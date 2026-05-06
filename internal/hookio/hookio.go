// Package hookio handles JSON I/O for Claude Code hook lifecycle events.
//
// Input shape (PreToolUse, abridged):
//
//	{
//	  "session_id": "abc-123",
//	  "transcript_path": "...",
//	  "cwd": "/path/to/project",
//	  "hook_event_name": "PreToolUse",
//	  "tool_name": "Bash",
//	  "tool_input": { "command": "...", "description": "..." }
//	}
//
// Output shapes:
//
//	{"decision":"approve"}                    // permit
//	{"decision":"approve","reason":"..."}     // permit + diagnostic reason
//	{"decision":"block","reason":"..."}       // deny + reason shown to Claude
//	(empty stdout)                            // also permits (Claude default)
//
// Notification / Stop / PostToolUse hooks accept the same Input shape
// (with extra fields like Message / ToolResponse) and don't require a
// structured response; arbiter still drains stdin to avoid EPIPE on the
// writer side.
package hookio

import (
	"encoding/json"
	"errors"
	"io"
)

// Input is the canonical shape parsed from hook stdin.
//
// Not all fields are populated for every hook event. Missing fields are
// the zero value; callers should check the relevant fields per event.
type Input struct {
	SessionID      string          `json:"session_id,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	Cwd            string          `json:"cwd,omitempty"`
	HookEventName  string          `json:"hook_event_name,omitempty"`

	// PreToolUse + PostToolUse
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`

	// Notification
	Message string `json:"message,omitempty"`
}

// Output is what arbiter writes to stdout for the calling CLI to read.
type Output struct {
	Decision string         `json:"decision,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ReadInput parses a single JSON object from r.
//
// Returns an error if r is empty or the body is not valid JSON. Callers
// in hook handlers should fail-open on error rather than propagating.
func ReadInput(r io.Reader) (*Input, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("hookio: empty input")
	}
	var in Input
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// WriteApprove emits a permit response. Optional reason is included as
// a diagnostic; it does not surface as a block message to the agent.
func WriteApprove(w io.Writer, reason string) error {
	out := Output{Decision: "approve"}
	if reason != "" {
		out.Reason = reason
	}
	return write(w, out)
}

// WriteBlock emits a deny response. The reason is shown to the calling
// agent so it can adapt its plan.
func WriteBlock(w io.Writer, reason string) error {
	return write(w, Output{Decision: "block", Reason: reason})
}

// WriteWithMetadata emits a decision with arbitrary metadata (used by
// later steps for fast-path / slow-path attribution).
func WriteWithMetadata(w io.Writer, decision, reason string, meta map[string]any) error {
	return write(w, Output{Decision: decision, Reason: reason, Metadata: meta})
}

func write(w io.Writer, out Output) error {
	enc := json.NewEncoder(w)
	return enc.Encode(out)
}
