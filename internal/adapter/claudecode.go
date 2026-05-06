package adapter

import (
	"context"
	"encoding/json"
)

// ClaudeCode is the reference adapter — Claude Code's hook payload is
// already very close to our canonical shape, so translation is mostly
// renaming.
type ClaudeCode struct{}

// NewClaudeCode constructs the adapter.
func NewClaudeCode() *ClaudeCode { return &ClaudeCode{} }

// Name implements Adapter.
func (ClaudeCode) Name() string { return "claude-code" }

// Capabilities implements Adapter.
func (ClaudeCode) Capabilities() Capabilities {
	return Capabilities{
		PreTool: true, PostTool: true, Notification: true, Stop: true,
		UserPrompt: true, MCP: true, Proxy: true,
	}
}

type ccRaw struct {
	SessionID     string          `json:"session_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	Message       string          `json:"message"`
	Prompt        string          `json:"prompt"`
}

// ParseEvent implements Adapter.
func (ClaudeCode) ParseEvent(_ context.Context, raw []byte) (CanonicalEvent, error) {
	var r ccRaw
	if err := json.Unmarshal(raw, &r); err != nil {
		return CanonicalEvent{}, err
	}
	ev := CanonicalEvent{
		SessionID: r.SessionID,
		CLI:       "claude-code",
		Cwd:       r.Cwd,
		Phase:     mapPhase(r.HookEventName),
		Tool:      Tool{Name: r.ToolName, Input: r.ToolInput},
		Metadata:  map[string]any{},
		Raw:       raw,
	}
	if r.Message != "" {
		ev.Metadata["message"] = r.Message
	}
	if r.Prompt != "" {
		ev.Metadata["prompt"] = r.Prompt
	}
	return ev, nil
}

func mapPhase(hookEventName string) Phase {
	switch hookEventName {
	case "PreToolUse":
		return PhasePreTool
	case "PostToolUse":
		return PhasePostTool
	case "Notification":
		return PhaseNotification
	case "Stop":
		return PhaseStop
	case "UserPromptSubmit":
		return PhaseUserPrompt
	}
	return ""
}

type ccDecision struct {
	HookSpecificOutput map[string]any `json:"hookSpecificOutput,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

// EncodeDecision implements Adapter.
func (ClaudeCode) EncodeDecision(_ context.Context, ev CanonicalEvent, d Decision) ([]byte, error) {
	out := ccDecision{
		HookSpecificOutput: map[string]any{
			"hookEventName":            mapHookEventName(ev.Phase),
			"permissionDecision":       d.Outcome,
			"permissionDecisionReason": d.Reason,
		},
		Metadata: d.Metadata,
	}
	if len(d.Updated) > 0 {
		var ui map[string]any
		if err := json.Unmarshal(d.Updated, &ui); err == nil {
			out.HookSpecificOutput["updatedInput"] = ui
		}
	}
	return json.Marshal(out)
}

func mapHookEventName(p Phase) string {
	switch p {
	case PhasePreTool:
		return "PreToolUse"
	case PhasePostTool:
		return "PostToolUse"
	case PhaseNotification:
		return "Notification"
	case PhaseStop:
		return "Stop"
	case PhaseUserPrompt:
		return "UserPromptSubmit"
	}
	return ""
}
