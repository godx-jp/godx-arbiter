package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/godx-team/godx-arbiter/internal/notify"
)

// EscalateToUser is a thin tool wrapper around notify.Escalate so the
// agent can reach the user mid-decision via its tool-use loop.
type EscalateToUser struct{}

// NewEscalateToUser constructs the tool.
func NewEscalateToUser() *EscalateToUser { return &EscalateToUser{} }

// Name implements Tool.
func (EscalateToUser) Name() string { return "escalate_to_user" }

// Description implements Tool.
func (EscalateToUser) Description() string {
	return "Send a question to the human user via configured notification channel(s). Blocks until reply or timeout. Returns reply, channel, elapsed_ms, user."
}

// InputSchema implements Tool.
func (EscalateToUser) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question":         map[string]any{"type": "string"},
			"options":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "default": []string{"approve", "deny"}},
			"context":          map[string]any{"type": "object"},
			"channel":          map[string]any{"type": "string", "description": "Override default notify_channels"},
			"timeout_seconds":  map[string]any{"type": "integer", "default": 60},
		},
		"required": []string{"question"},
	}
}

type escalateInput struct {
	Question       string         `json:"question"`
	Options        []string       `json:"options"`
	Context        map[string]any `json:"context"`
	Channel        string         `json:"channel"`
	TimeoutSeconds int            `json:"timeout_seconds"`
}

// Execute implements Tool.
func (e *EscalateToUser) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in escalateInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.Question == "" {
		return nil, fmt.Errorf("question is required")
	}
	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	channels := []string{}
	if in.Channel != "" {
		channels = append(channels, in.Channel)
	}
	reply, err := notify.Escalate(ctx, notify.EscalateRequest{
		Channels: channels,
		Question: in.Question,
		Options:  in.Options,
		Context:  in.Context,
		Timeout:  timeout,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(reply)
}
