package tools

import (
	"context"
	"encoding/json"

	"github.com/godx-team/godx-arbiter/internal/eventlog"
)

// LookupHistory queries the eventlog for similar past decisions so the
// agent can stay consistent across runs.
type LookupHistory struct{}

// NewLookupHistory constructs the tool.
func NewLookupHistory() *LookupHistory { return &LookupHistory{} }

// Name implements Tool.
func (LookupHistory) Name() string { return "lookup_history" }

// Description implements Tool.
func (LookupHistory) Description() string {
	return "Find recent decisions in the eventlog matching tool/pattern. Returns up to 'limit' matches, most recent first."
}

// InputSchema implements Tool.
func (LookupHistory) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool":    map[string]any{"type": "string"},
			"pattern": map[string]any{"type": "string"},
			"session": map[string]any{"type": "string"},
			"limit":   map[string]any{"type": "integer", "default": 5},
		},
	}
}

type lookupHistoryInput struct {
	Tool      string `json:"tool"`
	Pattern   string `json:"pattern"`
	SessionID string `json:"session"`
	Limit     int    `json:"limit"`
}

type lookupHistoryOutput struct {
	Matches []lookupMatch `json:"matches"`
	Count   int           `json:"count"`
}

type lookupMatch struct {
	TS        string `json:"ts"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason,omitempty"`
	SessionID string `json:"session,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Input     string `json:"input,omitempty"`
}

// Execute implements Tool.
func (l *LookupHistory) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in lookupHistoryInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	events, err := eventlog.Lookup(eventlog.LookupOpts{
		Tool: in.Tool, Pattern: in.Pattern, SessionID: in.SessionID, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := lookupHistoryOutput{Count: len(events)}
	for _, ev := range events {
		out.Matches = append(out.Matches, lookupMatch{
			TS: ev.TS.Format("2006-01-02T15:04:05Z07:00"),
			Decision: ev.Decision, Reason: ev.Reason,
			SessionID: ev.SessionID, Tool: ev.Tool, Input: ev.InputSum,
		})
	}
	return json.Marshal(out)
}
