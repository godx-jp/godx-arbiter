// Package policy evaluates fast-path rules from a parsed policy.yaml
// against an Action (a tool call from a coding-CLI session).
//
// The fast-path is regex-only and runs before the slow-path LLM agent.
// It exists to short-circuit the obvious cases — read-only commands,
// rm -rf of system paths, etc. — without paying any LLM cost. Anything
// the fast-path doesn't decide falls through to the agent.
//
// See docs/POLICY_SPEC.md for the user-facing schema and
// docs/DECISION_FLOW.md for how this fits into the bigger picture.
package policy

import "encoding/json"

// Outcome is the result of evaluating an Action against a Policy.
type Outcome int

const (
	// OutcomeAgent means "no fast-path decision; route to slow-path
	// agent". The default when no rule matches and the policy's
	// `default:` is `agent`.
	OutcomeAgent Outcome = iota
	// OutcomeAllow means "approve immediately; do not call the agent".
	OutcomeAllow
	// OutcomeDeny means "block immediately; reason will be shown to the
	// caller".
	OutcomeDeny
)

func (o Outcome) String() string {
	switch o {
	case OutcomeAllow:
		return "allow"
	case OutcomeDeny:
		return "deny"
	case OutcomeAgent:
		return "agent"
	default:
		return "unknown"
	}
}

// Decision captures the fast-path eval result with enough metadata for
// downstream logging, escalation, and `arbiter explain`.
type Decision struct {
	Outcome Outcome `json:"outcome"`

	// RuleType is one of: "deny", "allow", "to_agent", "default",
	// "no-policy". Identifies which list (if any) produced the
	// outcome.
	RuleType string `json:"rule_type"`

	// RuleIndex is the position of the matched rule within its list.
	// -1 when no specific rule matched (default fall-through, missing
	// policy).
	RuleIndex int `json:"rule_index"`

	// Reason is the rule's `reason` field (human-readable). Surfaced
	// to the calling agent on deny; useful for logs on allow.
	Reason string `json:"reason,omitempty"`

	// Pattern is the regex that matched, for explainability. Empty
	// when the rule was a tool-only match (no `pattern:`).
	Pattern string `json:"pattern,omitempty"`
}

// Action is what a hook (or proxy interception) feeds into Eval.
type Action struct {
	// ToolName is the canonical Claude-Code-style tool name: Bash,
	// Read, Edit, Write, Glob, Grep, Task, etc. For non-Claude CLIs,
	// adapters normalize to this set.
	ToolName string

	// ToolInput is the raw JSON of the tool call's input as the CLI
	// produced it. Eval extracts specific fields based on the rule's
	// `field:` selector (or a sensible default per tool).
	ToolInput json.RawMessage
}
