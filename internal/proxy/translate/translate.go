// Package translate handles cross-provider format translation between
// Anthropic, OpenAI, and Google Gemini request bodies.
//
// Scope: messages, system prompts, tool definitions, tool calls, tool
// results. Streaming is **not** translated — the proxy re-streams the
// upstream's chunks as-is, accepting that cross-provider streaming
// translation is out of scope for v1.
//
// Lossy edges are documented per-call: e.g., Anthropic `thinking`
// blocks have no OpenAI equivalent, and we drop them with a flag rather
// than silently dropping data (per docs/MODEL_ROUTING.md anti-patterns).
package translate

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Provider names a target API surface.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderGemini    Provider = "gemini"
)

// AnthropicReq is the subset of Anthropic /v1/messages we translate.
type AnthropicReq struct {
	Model     string             `json:"model"`
	System    json.RawMessage    `json:"system,omitempty"`
	Messages  []AnthropicMessage `json:"messages"`
	MaxTokens int64              `json:"max_tokens,omitempty"`
	Tools     []AnthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// OpenAIReq is the subset of OpenAI /v1/chat/completions we translate.
type OpenAIReq struct {
	Model     string          `json:"model"`
	Messages  []OpenAIMessage `json:"messages"`
	MaxTokens int64           `json:"max_tokens,omitempty"`
	Tools     []OpenAITool    `json:"tools,omitempty"`
	Stream    bool            `json:"stream,omitempty"`
}

type OpenAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type OpenAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type OpenAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// AnthropicToOpenAI translates a parsed Anthropic request into an
// OpenAI Chat Completions request. Returns the translated body + a
// slice of warning strings (lossy fields skipped).
func AnthropicToOpenAI(req AnthropicReq) (OpenAIReq, []string, error) {
	out := OpenAIReq{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    req.Stream,
	}
	var warnings []string

	if len(req.System) > 0 {
		sys, w := stringFromMaybeBlocks(req.System)
		warnings = append(warnings, w...)
		if sys != "" {
			out.Messages = append(out.Messages, OpenAIMessage{Role: "system", Content: jsonString(sys)})
		}
	}

	for _, m := range req.Messages {
		converted, w, err := anthropicMsgToOpenAI(m)
		if err != nil {
			return OpenAIReq{}, warnings, err
		}
		warnings = append(warnings, w...)
		out.Messages = append(out.Messages, converted...)
	}

	for _, t := range req.Tools {
		ot := OpenAITool{Type: "function"}
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.InputSchema
		out.Tools = append(out.Tools, ot)
	}
	return out, warnings, nil
}

// OpenAIToAnthropic is the inverse direction.
func OpenAIToAnthropic(req OpenAIReq) (AnthropicReq, []string, error) {
	out := AnthropicReq{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    req.Stream,
	}
	var warnings []string
	var system string
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			s, _ := stringFromMaybeBlocks(m.Content)
			if system == "" {
				system = s
			} else {
				system += "\n\n" + s
			}
			continue
		}
		blocks, w, err := openaiMsgToAnthropic(m)
		if err != nil {
			return AnthropicReq{}, warnings, err
		}
		warnings = append(warnings, w...)
		raw, err := json.Marshal(blocks)
		if err != nil {
			return AnthropicReq{}, warnings, err
		}
		out.Messages = append(out.Messages, AnthropicMessage{Role: m.Role, Content: raw})
	}
	if system != "" {
		out.System = jsonString(system)
	}
	for _, ot := range req.Tools {
		out.Tools = append(out.Tools, AnthropicTool{
			Name:        ot.Function.Name,
			Description: ot.Function.Description,
			InputSchema: ot.Function.Parameters,
		})
	}
	return out, warnings, nil
}

func anthropicMsgToOpenAI(m AnthropicMessage) ([]OpenAIMessage, []string, error) {
	// Anthropic content can be a string or an array of blocks. Try
	// string first.
	var asStr string
	if json.Unmarshal(m.Content, &asStr) == nil {
		return []OpenAIMessage{{Role: m.Role, Content: jsonString(asStr)}}, nil, nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, nil, fmt.Errorf("translate: anthropic content not string nor blocks: %w", err)
	}

	var warnings []string
	var text string
	var toolCalls []OpenAIToolCall
	var resultMessages []OpenAIMessage
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			if t, ok := b["text"].(string); ok {
				text += t
			}
		case "thinking", "redacted_thinking":
			warnings = append(warnings, "translate: anthropic 'thinking' block dropped — no OpenAI equivalent")
		case "tool_use":
			id, _ := b["id"].(string)
			name, _ := b["name"].(string)
			args, _ := b["input"].(map[string]any)
			argRaw, _ := json.Marshal(args)
			tc := OpenAIToolCall{ID: id, Type: "function"}
			tc.Function.Name = name
			tc.Function.Arguments = string(argRaw)
			toolCalls = append(toolCalls, tc)
		case "tool_result":
			id, _ := b["tool_use_id"].(string)
			content := stringifyToolResultContent(b["content"])
			resultMessages = append(resultMessages, OpenAIMessage{
				Role: "tool", ToolCallID: id, Content: jsonString(content),
			})
		}
	}
	out := []OpenAIMessage{}
	if text != "" || len(toolCalls) > 0 {
		out = append(out, OpenAIMessage{Role: m.Role, Content: jsonString(text), ToolCalls: toolCalls})
	}
	out = append(out, resultMessages...)
	return out, warnings, nil
}

func openaiMsgToAnthropic(m OpenAIMessage) ([]map[string]any, []string, error) {
	var blocks []map[string]any
	switch m.Role {
	case "tool":
		// OpenAI represents tool results as a flat message; Anthropic
		// wraps them in a tool_result block with tool_use_id.
		var content string
		_ = json.Unmarshal(m.Content, &content)
		blocks = append(blocks, map[string]any{
			"type":          "tool_result",
			"tool_use_id":   m.ToolCallID,
			"content":       content,
		})
		return blocks, nil, nil
	case "assistant", "user":
		var content string
		_ = json.Unmarshal(m.Content, &content)
		if content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": content})
		}
		for _, tc := range m.ToolCalls {
			var args any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			if args == nil {
				args = map[string]any{}
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Function.Name,
				"input": args,
			})
		}
		return blocks, nil, nil
	}
	return nil, nil, errors.New("translate: unknown openai role: " + m.Role)
}

func stringFromMaybeBlocks(raw json.RawMessage) (string, []string) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var blocks []map[string]any
	if json.Unmarshal(raw, &blocks) != nil {
		return "", nil
	}
	var out string
	var warnings []string
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			t, _ := b["text"].(string)
			out += t
		default:
			warnings = append(warnings, fmt.Sprintf("translate: dropped non-text system block %v", b["type"]))
		}
	}
	return out, warnings
}

func stringifyToolResultContent(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []any:
		out := ""
		for _, item := range s {
			if m, ok := item.(map[string]any); ok {
				if m["type"] == "text" {
					if t, ok := m["text"].(string); ok {
						out += t
					}
				}
			}
		}
		return out
	}
	if v == nil {
		return ""
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

func jsonString(s string) json.RawMessage {
	raw, _ := json.Marshal(s)
	return raw
}

// Cost models — not exhaustive, easy to extend. Source: provider
// public pricing tables, USD per million tokens, as of 2026-05.
var costPerMTokens = map[string]struct {
	Input, Output float64
}{
	"claude-haiku-4-5-20251001": {1, 5},
	"claude-sonnet-4-6":         {3, 15},
	"claude-opus-4-7":           {15, 75},
	"gpt-5":                     {5, 25},
	"gemini-2.5-flash":          {0.5, 2},
	"gemini-2.5-pro":            {3.5, 17.5},
}

// EstimateCost returns a rough cost estimate. Returns 0 for unknown
// models — better to under-bill than to fabricate.
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	c, ok := costPerMTokens[model]
	if !ok {
		return 0
	}
	return c.Input*float64(inputTokens)/1e6 + c.Output*float64(outputTokens)/1e6
}
