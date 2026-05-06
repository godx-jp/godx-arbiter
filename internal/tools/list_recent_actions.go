package tools

import (
	"context"
	"encoding/json"

	"github.com/godx-team/godx-arbiter/internal/eventlog"
)

// ListRecentActions returns the latest events in the current session.
// Useful for "is this the next step of an already-approved refactor?"
type ListRecentActions struct{}

// NewListRecentActions constructs the tool.
func NewListRecentActions() *ListRecentActions { return &ListRecentActions{} }

// Name implements Tool.
func (ListRecentActions) Name() string { return "list_recent_actions" }

// Description implements Tool.
func (ListRecentActions) Description() string {
	return "List the most recent decisions for a session (default: limit 20)."
}

// InputSchema implements Tool.
func (ListRecentActions) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"limit":      map[string]any{"type": "integer", "default": 20},
		},
		"required": []string{"session_id"},
	}
}

type listRecentInput struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit"`
}

// Execute implements Tool.
func (l *ListRecentActions) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in listRecentInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	events, err := eventlog.Lookup(eventlog.LookupOpts{SessionID: in.SessionID, Limit: limit})
	if err != nil {
		return nil, err
	}
	type entry struct {
		TS       string `json:"ts"`
		Tool     string `json:"tool"`
		Input    string `json:"input"`
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}
	out := struct {
		Actions []entry `json:"actions"`
	}{}
	for _, ev := range events {
		out.Actions = append(out.Actions, entry{
			TS: ev.TS.UTC().Format("2006-01-02T15:04:05Z"),
			Tool: ev.Tool, Input: ev.InputSum, Decision: ev.Decision, Reason: ev.Reason,
		})
	}
	return json.Marshal(out)
}
