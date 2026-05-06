// Package adapter normalizes hook + proxy events from each supported
// CLI into a single CanonicalEvent so the decide pipeline doesn't need
// to know which CLI sent it.
//
// The Claude Code adapter is the reference (richest event surface).
// Codex / Gemini / Antigravity adapters are thinner and best-effort —
// they extract what the CLI exposes and accept that some fields may be
// empty.
package adapter

import (
	"context"
	"encoding/json"
)

// Phase is the lifecycle stage of a tool call.
type Phase string

const (
	PhasePreTool      Phase = "pre_tool"
	PhasePostTool     Phase = "post_tool"
	PhaseNotification Phase = "notification"
	PhaseStop         Phase = "stop"
	PhaseUserPrompt   Phase = "user_prompt"
)

// Tool is the normalized tool call shape.
type Tool struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

// CanonicalEvent is what the decide pipeline consumes regardless of CLI.
type CanonicalEvent struct {
	SessionID string         `json:"session_id"`
	CLI       string         `json:"cli"`
	Cwd       string         `json:"cwd,omitempty"`
	Phase     Phase          `json:"phase"`
	Tool      Tool           `json:"tool,omitempty"`
	ModelHint string         `json:"model_hint,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// Decision is the canonical decide outcome.
type Decision struct {
	Outcome  string         // "allow" | "deny" | "ask" | "defer"
	Reason   string
	Updated  json.RawMessage // optional rewritten tool_input
	Metadata map[string]any
}

// Capabilities describes what a CLI's hook surface supports. Used by
// `arbiter init` and `arbiter doctor` to skip features the CLI can't
// receive.
type Capabilities struct {
	PreTool      bool
	PostTool     bool
	Notification bool
	Stop         bool
	UserPrompt   bool
	MCP          bool
	Proxy        bool
}

// Adapter is the per-CLI translator.
type Adapter interface {
	Name() string
	Capabilities() Capabilities
	ParseEvent(ctx context.Context, raw []byte) (CanonicalEvent, error)
	EncodeDecision(ctx context.Context, ev CanonicalEvent, d Decision) ([]byte, error)
}

// Registry maps CLI name → adapter.
type Registry struct {
	adapters map[string]Adapter
}

// NewRegistry builds a registry seeded with all built-in adapters.
func NewRegistry() *Registry {
	r := &Registry{adapters: map[string]Adapter{}}
	r.Register(NewClaudeCode())
	r.Register(NewCodex())
	r.Register(NewGemini())
	r.Register(NewAntigravity())
	return r
}

// Register installs an adapter (replacing any with the same name).
func (r *Registry) Register(a Adapter) {
	r.adapters[a.Name()] = a
}

// Get returns the adapter named.
func (r *Registry) Get(name string) (Adapter, bool) {
	a, ok := r.adapters[name]
	return a, ok
}

// All returns every registered adapter.
func (r *Registry) All() []Adapter {
	out := make([]Adapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a)
	}
	return out
}
