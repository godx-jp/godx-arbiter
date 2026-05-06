package translate

import (
	"encoding/json"
	"fmt"
)

// GeminiReq is the subset of Google Generative Language we translate
// (v1beta /models/<m>:generateContent). The full schema is much
// larger; we cover what real CLI traffic exercises (system, contents,
// tools, function-calls / function-responses).
type GeminiReq struct {
	SystemInstruction *GeminiContent      `json:"system_instruction,omitempty"`
	Contents          []GeminiContent     `json:"contents"`
	Tools             []GeminiTool        `json:"tools,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}

// GeminiContent is one role + parts pair (Gemini calls it
// "content"; the parts can be text, function-call, function-response,
// inline-data).
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart is the per-block payload — exactly one of the inner
// fields is set.
type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
}

// GeminiFunctionCall = OpenAI tool_calls entry / Anthropic tool_use.
type GeminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// GeminiFunctionResponse = OpenAI tool message / Anthropic tool_result.
type GeminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// GeminiTool wraps the function declaration set. Gemini groups
// declarations under a single tool; OpenAI / Anthropic flatten them.
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDecl `json:"functionDeclarations"`
}

type GeminiFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type GeminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// AnthropicToGemini translates a parsed Anthropic request into Gemini's
// generateContent shape.
func AnthropicToGemini(req AnthropicReq) (GeminiReq, []string, error) {
	out := GeminiReq{}
	var warnings []string

	if len(req.System) > 0 {
		s, w := stringFromMaybeBlocks(req.System)
		warnings = append(warnings, w...)
		if s != "" {
			out.SystemInstruction = &GeminiContent{Parts: []GeminiPart{{Text: s}}}
		}
	}
	for _, m := range req.Messages {
		c, w, err := anthropicMsgToGemini(m)
		if err != nil {
			return GeminiReq{}, warnings, err
		}
		warnings = append(warnings, w...)
		if c != nil {
			out.Contents = append(out.Contents, *c)
		}
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, GeminiTool{
			FunctionDeclarations: []GeminiFunctionDecl{{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			}},
		})
	}
	if req.MaxTokens > 0 {
		out.GenerationConfig = &GeminiGenerationConfig{MaxOutputTokens: int(req.MaxTokens)}
	}
	return out, warnings, nil
}

// GeminiToAnthropic is the inverse direction.
func GeminiToAnthropic(req GeminiReq) (AnthropicReq, []string, error) {
	out := AnthropicReq{}
	var warnings []string
	if req.SystemInstruction != nil {
		out.System = jsonString(joinTextParts(req.SystemInstruction.Parts))
	}
	for _, c := range req.Contents {
		blocks, w, err := geminiContentToAnthropic(c)
		if err != nil {
			return AnthropicReq{}, warnings, err
		}
		warnings = append(warnings, w...)
		raw, err := json.Marshal(blocks)
		if err != nil {
			return AnthropicReq{}, warnings, err
		}
		out.Messages = append(out.Messages, AnthropicMessage{Role: anthropicRole(c.Role), Content: raw})
	}
	for _, t := range req.Tools {
		for _, fd := range t.FunctionDeclarations {
			out.Tools = append(out.Tools, AnthropicTool{
				Name: fd.Name, Description: fd.Description, InputSchema: fd.Parameters,
			})
		}
	}
	if req.GenerationConfig != nil {
		out.MaxTokens = int64(req.GenerationConfig.MaxOutputTokens)
	}
	return out, warnings, nil
}

// OpenAIToGemini chains via Anthropic to keep one canonical hop. Lossy
// fields are surfaced as warnings either way.
func OpenAIToGemini(req OpenAIReq) (GeminiReq, []string, error) {
	mid, w1, err := OpenAIToAnthropic(req)
	if err != nil {
		return GeminiReq{}, w1, err
	}
	out, w2, err := AnthropicToGemini(mid)
	return out, append(w1, w2...), err
}

// GeminiToOpenAI is the inverse.
func GeminiToOpenAI(req GeminiReq) (OpenAIReq, []string, error) {
	mid, w1, err := GeminiToAnthropic(req)
	if err != nil {
		return OpenAIReq{}, w1, err
	}
	out, w2, err := AnthropicToOpenAI(mid)
	return out, append(w1, w2...), err
}

func anthropicMsgToGemini(m AnthropicMessage) (*GeminiContent, []string, error) {
	role := geminiRole(m.Role)
	c := &GeminiContent{Role: role}
	var warnings []string

	var asStr string
	if json.Unmarshal(m.Content, &asStr) == nil {
		if asStr == "" {
			return nil, nil, nil
		}
		c.Parts = []GeminiPart{{Text: asStr}}
		return c, nil, nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, nil, fmt.Errorf("translate: gemini: anthropic content parse: %w", err)
	}
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			if t, ok := b["text"].(string); ok && t != "" {
				c.Parts = append(c.Parts, GeminiPart{Text: t})
			}
		case "thinking", "redacted_thinking":
			warnings = append(warnings, "translate: gemini: dropped Anthropic thinking block")
		case "tool_use":
			name, _ := b["name"].(string)
			args, _ := b["input"].(map[string]any)
			if args == nil {
				args = map[string]any{}
			}
			c.Parts = append(c.Parts, GeminiPart{FunctionCall: &GeminiFunctionCall{Name: name, Args: args}})
		case "tool_result":
			name, _ := b["tool_use_id"].(string) // best-effort: Gemini wants the function NAME, not the tool_use_id; CLIs that round-trip lose this. Documented warning:
			content := stringifyToolResultContent(b["content"])
			warnings = append(warnings, "translate: gemini: tool_result mapped to functionResponse using tool_use_id as name (Anthropic identifies tool_results by id, Gemini by name)")
			c.Parts = append(c.Parts, GeminiPart{
				FunctionResponse: &GeminiFunctionResponse{
					Name:     name,
					Response: map[string]any{"content": content},
				},
			})
		}
	}
	if len(c.Parts) == 0 {
		return nil, warnings, nil
	}
	return c, warnings, nil
}

func geminiContentToAnthropic(c GeminiContent) ([]map[string]any, []string, error) {
	var blocks []map[string]any
	for _, p := range c.Parts {
		switch {
		case p.Text != "":
			blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
		case p.FunctionCall != nil:
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    p.FunctionCall.Name + "-call", // Gemini lacks a dedicated id; synthesize one
				"name":  p.FunctionCall.Name,
				"input": p.FunctionCall.Args,
			})
		case p.FunctionResponse != nil:
			content := ""
			if p.FunctionResponse.Response != nil {
				if v, ok := p.FunctionResponse.Response["content"]; ok {
					if s, ok := v.(string); ok {
						content = s
					} else {
						raw, _ := json.Marshal(v)
						content = string(raw)
					}
				} else {
					raw, _ := json.Marshal(p.FunctionResponse.Response)
					content = string(raw)
				}
			}
			blocks = append(blocks, map[string]any{
				"type":        "tool_result",
				"tool_use_id": p.FunctionResponse.Name + "-call",
				"content":     content,
			})
		}
	}
	return blocks, nil, nil
}

func geminiRole(anth string) string {
	switch anth {
	case "assistant":
		return "model"
	default:
		return "user"
	}
}

func anthropicRole(gem string) string {
	switch gem {
	case "model":
		return "assistant"
	default:
		return "user"
	}
}

func joinTextParts(parts []GeminiPart) string {
	var s string
	for _, p := range parts {
		s += p.Text
	}
	return s
}
