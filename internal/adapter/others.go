package adapter

import (
	"context"
	"encoding/json"
)

// Codex / Gemini / Antigravity all share the "thin adapter" shape —
// they currently expose limited or no hook surface, so most usage
// lands in proxy mode (Mode B in docs/MULTI_CLI.md). Each adapter
// surfaces what its CLI documents today and ignores fields it can't
// guarantee.

// Codex adapts the OpenAI Codex CLI.
type Codex struct{}

// NewCodex constructs the adapter.
func NewCodex() *Codex { return &Codex{} }

// Name implements Adapter.
func (Codex) Name() string { return "codex" }

// Capabilities implements Adapter.
func (Codex) Capabilities() Capabilities {
	return Capabilities{PreTool: false, Stop: false, Proxy: true, MCP: true}
}

// ParseEvent implements Adapter.
func (Codex) ParseEvent(_ context.Context, raw []byte) (CanonicalEvent, error) {
	return parseGenericEvent("codex", raw)
}

// EncodeDecision implements Adapter — Codex consumes the hook output
// shape pioneered by Claude Code, so we reuse that encoder.
func (Codex) EncodeDecision(ctx context.Context, ev CanonicalEvent, d Decision) ([]byte, error) {
	return ClaudeCode{}.EncodeDecision(ctx, ev, d)
}

// Gemini adapts Google's Gemini CLI.
type Gemini struct{}

// NewGemini constructs the adapter.
func NewGemini() *Gemini { return &Gemini{} }

// Name implements Adapter.
func (Gemini) Name() string { return "gemini" }

// Capabilities implements Adapter.
func (Gemini) Capabilities() Capabilities {
	return Capabilities{Proxy: true, MCP: true}
}

// ParseEvent implements Adapter.
func (Gemini) ParseEvent(_ context.Context, raw []byte) (CanonicalEvent, error) {
	return parseGenericEvent("gemini", raw)
}

// EncodeDecision implements Adapter. Gemini's hook output uses
// {decision: "allow|deny|block", reason}; we map onto that shape and
// add a few extras the CLI tolerates.
func (Gemini) EncodeDecision(_ context.Context, _ CanonicalEvent, d Decision) ([]byte, error) {
	out := map[string]any{
		"decision": geminiOutcome(d.Outcome),
		"reason":   d.Reason,
		"metadata": d.Metadata,
	}
	return json.Marshal(out)
}

func geminiOutcome(o string) string {
	switch o {
	case "allow":
		return "allow"
	case "deny":
		return "block"
	case "ask":
		return "ask"
	}
	return "allow"
}

// Antigravity adapts Google's Antigravity tool.
type Antigravity struct{}

// NewAntigravity constructs the adapter.
func NewAntigravity() *Antigravity { return &Antigravity{} }

// Name implements Adapter.
func (Antigravity) Name() string { return "antigravity" }

// Capabilities implements Adapter.
func (Antigravity) Capabilities() Capabilities {
	return Capabilities{Proxy: true}
}

// ParseEvent implements Adapter.
func (Antigravity) ParseEvent(_ context.Context, raw []byte) (CanonicalEvent, error) {
	return parseGenericEvent("antigravity", raw)
}

// EncodeDecision implements Adapter — Antigravity is still in flux;
// reuse the Claude Code shape until the API stabilizes.
func (Antigravity) EncodeDecision(ctx context.Context, ev CanonicalEvent, d Decision) ([]byte, error) {
	return ClaudeCode{}.EncodeDecision(ctx, ev, d)
}

// parseGenericEvent best-effort parses an event from a non-Claude-Code
// CLI. Uses a permissive shape: anything we recognize is mapped, the
// rest goes into Metadata.
func parseGenericEvent(cli string, raw []byte) (CanonicalEvent, error) {
	var generic struct {
		SessionID string          `json:"session_id"`
		Cwd       string          `json:"cwd"`
		Phase     string          `json:"phase"`
		Event     string          `json:"event"`
		Tool      Tool            `json:"tool"`
		ModelHint string          `json:"model"`
		Metadata  map[string]any  `json:"metadata"`
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return CanonicalEvent{}, err
	}
	tool := generic.Tool
	if tool.Name == "" && generic.ToolName != "" {
		tool = Tool{Name: generic.ToolName, Input: generic.ToolInput}
	}
	phase := Phase(generic.Phase)
	if phase == "" {
		phase = mapPhase(generic.Event)
	}
	return CanonicalEvent{
		SessionID: generic.SessionID,
		CLI:       cli,
		Cwd:       generic.Cwd,
		Phase:     phase,
		Tool:      tool,
		ModelHint: generic.ModelHint,
		Metadata:  generic.Metadata,
		Raw:       raw,
	}, nil
}
