package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/godx-team/godx-arbiter/internal/adapter"
	policycfg "github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/policy"
	"github.com/godx-team/godx-arbiter/internal/proxy/budget"
	"github.com/godx-team/godx-arbiter/internal/proxy/classify"
	"github.com/godx-team/godx-arbiter/internal/proxy/route"
	"github.com/godx-team/godx-arbiter/internal/proxy/translate"
	"github.com/godx-team/godx-arbiter/internal/usage"
)

// Wiring bundles together the moving parts a fully-equipped proxy
// needs: routing table, classifier, fast-path policy, and budget.
//
// Construct one and pass `Wiring.Hooks()` to Server.WithHooks.
type Wiring struct {
	Routing  *route.Table
	Policy   *policycfg.Policy
	Budget   *budget.State
	OnStatus func(provider, model string, status budget.Status)
	Project  string
	CLI      string
}

// NewWiring assembles defaults from a project config.
func NewWiring(proj *policycfg.Project, cli string) *Wiring {
	w := &Wiring{
		CLI:    cli,
		Budget: budget.NewState(budget.Limits{}),
	}
	if proj != nil {
		w.Project = proj.Root
		if proj.Policy != nil {
			w.Policy = proj.Policy
		}
		if proj.Rules != nil {
			w.Routing = route.ParseSection(proj.Rules.Body)
		}
	}
	if w.Routing == nil {
		w.Routing = &route.Table{}
	}
	w.Budget.HydrateFromLedger()
	return w
}

// Hooks returns Server.Hooks that apply tool gating + routing + budget.
func (w *Wiring) Hooks() Hooks {
	return Hooks{
		PreForward:   w.preForward,
		PostResponse: w.postResponse,
	}
}

func (w *Wiring) preForward(provider string, body []byte, header http.Header) ([]byte, http.Header, http.Header, error) {
	switch provider {
	case "anthropic":
		return w.preForwardAnthropic(body, header)
	case "openai":
		return w.preForwardOpenAI(body, header)
	}
	return body, header, nil, nil
}

func (w *Wiring) preForwardAnthropic(body []byte, header http.Header) ([]byte, http.Header, http.Header, error) {
	var req translate.AnthropicReq
	if err := json.Unmarshal(body, &req); err != nil {
		return body, header, nil, nil // pass through unchanged on parse fail
	}
	tag, _ := classify.Classify(classify.Input{
		UserMessage: lastUserText(req.Messages),
		ToolNames:   anthropicToolNames(req.Messages),
	})
	respHeaders := http.Header{}
	picked, reason := w.Routing.Pick(w.CLI, req.Model, tag)
	if picked != "" && picked != req.Model {
		respHeaders.Set("X-Arbiter-Routed-From", req.Model)
		respHeaders.Set("X-Arbiter-Routed-To", picked)
		if reason != "" {
			respHeaders.Set("X-Arbiter-Routed-Reason", reason)
		}
		req.Model = picked
		body, _ = json.Marshal(req)
	}
	if w.Budget != nil {
		if status := w.Budget.Inspect(""); status.OverHard {
			return nil, nil, nil, fmt.Errorf("budget hard limit reached: %s", status.Reason)
		}
	}
	return body, header, respHeaders, nil
}

func (w *Wiring) preForwardOpenAI(body []byte, header http.Header) ([]byte, http.Header, http.Header, error) {
	var req translate.OpenAIReq
	if err := json.Unmarshal(body, &req); err != nil {
		return body, header, nil, nil
	}
	tag, _ := classify.Classify(classify.Input{
		UserMessage: lastOpenAIUserText(req.Messages),
		ToolNames:   openaiToolNames(req.Messages),
	})
	respHeaders := http.Header{}
	picked, reason := w.Routing.Pick(w.CLI, req.Model, tag)
	if picked != "" && picked != req.Model {
		respHeaders.Set("X-Arbiter-Routed-From", req.Model)
		respHeaders.Set("X-Arbiter-Routed-To", picked)
		if reason != "" {
			respHeaders.Set("X-Arbiter-Routed-Reason", reason)
		}
		req.Model = picked
		body, _ = json.Marshal(req)
	}
	if w.Budget != nil {
		if status := w.Budget.Inspect(""); status.OverHard {
			return nil, nil, nil, fmt.Errorf("budget hard limit reached: %s", status.Reason)
		}
	}
	return body, header, respHeaders, nil
}

func (w *Wiring) postResponse(provider string, requestBody, responseBody []byte) ([]byte, error) {
	model, sessionID, in, out := extractUsage(provider, requestBody, responseBody)
	if in+out > 0 {
		_ = usage.Append(usage.Record{
			SessionID: sessionID, CLI: w.CLI, Provider: provider, Model: model,
			InputTokens: in, OutputTokens: out, CostUSD: translate.EstimateCost(model, in, out),
			Path: "proxy", Project: w.Project,
		})
		if w.Budget != nil {
			status, _ := w.Budget.Charge(sessionID, in, out, translate.EstimateCost(model, in, out))
			if w.OnStatus != nil {
				w.OnStatus(provider, model, status)
			}
		}
	}

	// Tool gating (Step 12): decode tool_use blocks and apply fast-path
	// policy. On deny, rewrite the response so the CLI sees a synthetic
	// tool_result with isError = true rather than the requested tool_use.
	switch provider {
	case "anthropic":
		return w.gateAnthropicResponse(responseBody)
	case "openai":
		return w.gateOpenAIResponse(responseBody)
	}
	return responseBody, nil
}

// gateAnthropicResponse looks for tool_use blocks in a /v1/messages
// response and applies the fast-path policy.  When a tool is denied,
// the block is rewritten in-place to a tool_result-shaped synthetic
// error so the calling agent can recover.
func (w *Wiring) gateAnthropicResponse(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, nil
	}
	contentRaw, _ := resp["content"].([]any)
	if len(contentRaw) == 0 || w.Policy == nil {
		return body, nil
	}
	rewritten := false
	for i, block := range contentRaw {
		m, _ := block.(map[string]any)
		if m == nil || m["type"] != "tool_use" {
			continue
		}
		name, _ := m["name"].(string)
		input, _ := json.Marshal(m["input"])
		d := policy.Eval(w.Policy, &policy.Action{ToolName: name, ToolInput: input})
		if d.Outcome != policy.OutcomeDeny {
			continue
		}
		contentRaw[i] = map[string]any{
			"type":    "text",
			"text":    fmt.Sprintf("tool refused by godx-arbiter: %s", chooseReason(d.Reason, "denied by policy")),
			"arbiter": map[string]any{"refused_tool": name, "reason": d.Reason},
		}
		rewritten = true
	}
	if !rewritten {
		return body, nil
	}
	resp["content"] = contentRaw
	out, _ := json.Marshal(resp)
	return out, nil
}

func (w *Wiring) gateOpenAIResponse(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, nil
	}
	choices, _ := resp["choices"].([]any)
	rewritten := false
	for ci, c := range choices {
		choice, _ := c.(map[string]any)
		msg, _ := choice["message"].(map[string]any)
		if msg == nil {
			continue
		}
		toolCalls, _ := msg["tool_calls"].([]any)
		if len(toolCalls) == 0 || w.Policy == nil {
			continue
		}
		for tci, tc := range toolCalls {
			m, _ := tc.(map[string]any)
			fn, _ := m["function"].(map[string]any)
			if fn == nil {
				continue
			}
			name, _ := fn["name"].(string)
			argStr, _ := fn["arguments"].(string)
			d := policy.Eval(w.Policy, &policy.Action{ToolName: name, ToolInput: json.RawMessage(argStr)})
			if d.Outcome != policy.OutcomeDeny {
				continue
			}
			toolCalls[tci] = map[string]any{
				"id":   m["id"],
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": fmt.Sprintf(`{"arbiter_refused":true,"reason":%q}`, chooseReason(d.Reason, "denied by policy")),
				},
			}
			rewritten = true
		}
		msg["tool_calls"] = toolCalls
		choice["message"] = msg
		choices[ci] = choice
	}
	if !rewritten {
		return body, nil
	}
	resp["choices"] = choices
	out, _ := json.Marshal(resp)
	return out, nil
}

func chooseReason(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func anthropicToolNames(msgs []translate.AnthropicMessage) []string {
	var out []string
	for _, m := range msgs {
		var asArr []map[string]any
		if err := json.Unmarshal(m.Content, &asArr); err != nil {
			continue
		}
		for _, b := range asArr {
			if b["type"] == "tool_use" {
				if n, ok := b["name"].(string); ok {
					out = append(out, n)
				}
			}
		}
	}
	return out
}

func openaiToolNames(msgs []translate.OpenAIMessage) []string {
	var out []string
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			out = append(out, tc.Function.Name)
		}
	}
	return out
}

func lastUserText(msgs []translate.AnthropicMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		var s string
		if err := json.Unmarshal(msgs[i].Content, &s); err == nil {
			return s
		}
		var blocks []map[string]any
		if err := json.Unmarshal(msgs[i].Content, &blocks); err == nil {
			for _, b := range blocks {
				if b["type"] == "text" {
					if t, ok := b["text"].(string); ok {
						return t
					}
				}
			}
		}
	}
	return ""
}

func lastOpenAIUserText(msgs []translate.OpenAIMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		var s string
		_ = json.Unmarshal(msgs[i].Content, &s)
		return s
	}
	return ""
}

func extractUsage(provider string, requestBody, responseBody []byte) (model, session string, in, out int) {
	switch provider {
	case "anthropic":
		var resp struct {
			ID       string `json:"id"`
			Model    string `json:"model"`
			Usage    struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(responseBody, &resp)
		return resp.Model, resp.ID, resp.Usage.InputTokens, resp.Usage.OutputTokens
	case "openai":
		var resp struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(responseBody, &resp)
		return resp.Model, resp.ID, resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	return "", "", 0, 0
}

// silence unused import linters when adapter isn't otherwise referenced
// in this file — kept to keep the package one-stop for proxy wiring.
var _ = adapter.PhasePreTool
