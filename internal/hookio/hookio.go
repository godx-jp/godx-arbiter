// Package hookio handles JSON I/O for hook lifecycle events from
// agentic coding CLIs (Claude Code, Codex CLI, Gemini CLI).
//
// Output schema is the modern hookSpecificOutput.permissionDecision
// shape (Claude Code Nov 2025+, Codex CLI 0.128+):
//
//	{
//	  "hookSpecificOutput": {
//	    "hookEventName": "PreToolUse",
//	    "permissionDecision": "allow|deny|ask|defer",
//	    "permissionDecisionReason": "...",
//	    "updatedInput": { ... },     // optional — for "edit" semantics
//	    "additionalContext": "..."   // optional — surfaced to the model
//	  },
//	  "metadata": { ... }            // arbiter-specific diagnostics
//	}
//
// The legacy top-level {"decision":"approve|block","reason":"..."}
// shape is still accepted by older versions; we don't emit it. Gemini
// CLI uses its own variant ({decision: "allow|deny|block", reason}
// + extras like continue/stopReason/systemMessage/suppressOutput);
// adapters in internal/adapter/<cli>/ translate when needed.
package hookio

import (
	"encoding/json"
	"errors"
	"io"
)

// Decision is the modern permissionDecision vocabulary.
type Decision string

const (
	// DecisionAllow approves the tool call.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks the tool call. The reason is shown to the
	// calling agent so it can adapt.
	DecisionDeny Decision = "deny"
	// DecisionAsk surfaces a permission prompt in the calling CLI's
	// own UI (Claude Code's permission dialog, Gemini's confirm prompt).
	DecisionAsk Decision = "ask"
	// DecisionDefer means "this hook has no opinion" — let downstream
	// hooks / the CLI's default policy decide. Useful when chaining
	// arbiter with other hooks.
	DecisionDefer Decision = "defer"
)

// Input is the canonical shape parsed from hook stdin.
//
// Fields are populated per-event; missing fields are zero-valued.
// The schema follows Claude Code's hook input; adapters for non-Claude
// CLIs translate their native shape into this canonical form before
// invoking the decision pipeline.
type Input struct {
	SessionID      string `json:"session_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	HookEventName  string `json:"hook_event_name,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`

	// Sub-agent context (Claude Code SubagentStart / SubagentStop).
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`

	// PreToolUse + PostToolUse
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`

	// Notification / Stop / UserPromptSubmit
	Message string `json:"message,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// HookSpecificOutput carries the modern permissionDecision payload.
type HookSpecificOutput struct {
	HookEventName            string         `json:"hookEventName,omitempty"`
	PermissionDecision       Decision       `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
	AdditionalContext        string         `json:"additionalContext,omitempty"`
}

// Output is the JSON written to stdout for the calling CLI to consume.
type Output struct {
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`

	// Metadata is arbiter-specific diagnostic info (rule matched,
	// duration, fast-path vs slow-path, etc.). Logged by the CLI but
	// not surfaced to the model.
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

// WriteAllow emits an allow decision for PreToolUse. Optional reason
// is surfaced to the model for context.
func WriteAllow(w io.Writer, reason string) error {
	return WriteAllowWithMeta(w, reason, nil)
}

// WriteAllowWithMeta is WriteAllow + arbiter metadata.
func WriteAllowWithMeta(w io.Writer, reason string, meta map[string]any) error {
	return write(w, Output{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       DecisionAllow,
			PermissionDecisionReason: reason,
		},
		Metadata: meta,
	})
}

// WriteDeny emits a deny decision. The reason is shown to the calling
// agent so it can adapt its plan.
func WriteDeny(w io.Writer, reason string) error {
	return WriteDenyWithMeta(w, reason, nil)
}

// WriteDenyWithMeta is WriteDeny + arbiter metadata.
func WriteDenyWithMeta(w io.Writer, reason string, meta map[string]any) error {
	return write(w, Output{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       DecisionDeny,
			PermissionDecisionReason: reason,
		},
		Metadata: meta,
	})
}

// WriteAsk asks the calling CLI to surface a confirmation prompt to
// the human via its own UI. Useful when arbiter has no rule one way
// or the other and prefers to delegate to the CLI's permission flow
// rather than its own escalation channel.
func WriteAsk(w io.Writer, reason string) error {
	return write(w, Output{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       DecisionAsk,
			PermissionDecisionReason: reason,
		},
	})
}

// WriteEdit approves the tool call but with a modified input. Borrowed
// from LangGraph's interrupt/resume vocabulary — lets arbiter sanitize
// the input rather than block-or-allow whole-cloth.
//
// Example: a Bash command with a sketchy flag combination — strip the
// flag and approve, instead of denying the whole command.
func WriteEdit(w io.Writer, updatedInput map[string]any, reason string) error {
	return write(w, Output{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       DecisionAllow,
			PermissionDecisionReason: reason,
			UpdatedInput:             updatedInput,
		},
	})
}

// WriteDefer emits a "no opinion" decision so downstream hooks /
// the CLI's default policy can decide.
func WriteDefer(w io.Writer) error {
	return write(w, Output{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:      "PreToolUse",
			PermissionDecision: DecisionDefer,
		},
	})
}

func write(w io.Writer, out Output) error {
	enc := json.NewEncoder(w)
	return enc.Encode(out)
}
