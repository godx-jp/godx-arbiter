// Package agent implements the slow-path LLM agent that decides
// approve / deny / ask when the fast-path policy can't.
//
// The agent:
//
//  1. Builds a system prompt from rules.md body + selected skills +
//     the action JSON.
//  2. Loops: call LLM → on tool-use, execute via tools.Registry → loop
//     again with tool result attached → end on text-only stop.
//  3. Parses the final assistant text for one of:
//
//        ARBITER_DECISION: approve [— reason]
//        ARBITER_DECISION: deny — reason
//        ARBITER_DECISION: ask — question
//
//  4. Records an eventlog entry (with full agent trace) before returning.
//
// The model surface is abstracted via the LLM interface so tests can
// substitute a deterministic mock without hitting the network.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/eventlog"
	"github.com/godx-team/godx-arbiter/internal/tools"
)

// Decision is the agent's final verdict.
type Decision struct {
	Outcome  string // "approve" | "deny" | "ask"
	Reason   string
	Question string // populated when Outcome == "ask"
	Trace    *eventlog.AgentTrace
}

// Action is the tool call under review.
type Action struct {
	SessionID string
	Project   string
	Cwd       string
	ToolName  string
	ToolInput json.RawMessage
}

// Config controls one Decide() invocation.
type Config struct {
	Model              string
	MaxTokens          int64
	MaxIterations      int
	Timeout            time.Duration
	Tools              *tools.Registry
	SkillTexts         []string // resolved skill bodies, in include order
	ProjectFrontMatter *config.RulesFrontMatter
	RulesBody          string
	OnTimeout          string // approve | deny | escalate (per rules.md FM)
	OnError            string // approve | deny (per rules.md FM)
}

// DefaultConfig returns sane defaults reading from rules.md front matter.
func DefaultConfig(r *config.Rules) Config {
	cfg := Config{
		Model:         "claude-haiku-4-5-20251001",
		MaxTokens:     1024,
		MaxIterations: 10,
		Timeout:       30 * time.Second,
		OnTimeout:     "deny",
		OnError:       "approve",
	}
	if r == nil {
		return cfg
	}
	fm := r.FrontMatter
	cfg.ProjectFrontMatter = &fm
	cfg.RulesBody = r.Body
	if fm.AgentModel != "" {
		cfg.Model = fm.AgentModel
	}
	if fm.TimeoutSeconds > 0 {
		cfg.Timeout = time.Duration(fm.TimeoutSeconds) * time.Second
	}
	if fm.MaxAgentIterations > 0 {
		cfg.MaxIterations = fm.MaxAgentIterations
	}
	if fm.OnTimeout != "" {
		cfg.OnTimeout = fm.OnTimeout
	}
	if fm.OnError != "" {
		cfg.OnError = fm.OnError
	}
	return cfg
}

// Agent runs the decide loop with a swappable LLM backend.
type Agent struct {
	llm LLM
}

// New constructs an Agent.
func New(llm LLM) *Agent { return &Agent{llm: llm} }

// Decide runs the slow-path decision loop. Always returns a Decision —
// timeouts and internal errors are translated to the rules.md fallback
// rather than propagating up to the hook (per ADR-005).
func (a *Agent) Decide(ctx context.Context, cfg Config, action Action) Decision {
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	sys := buildSystemPrompt(cfg, action)
	user := []Turn{{Role: "user", Blocks: []Block{{Type: BlockText, Text: userInstruction()}}}}
	toolDefs := buildToolDefs(cfg.Tools)

	trace := &eventlog.AgentTrace{Model: cfg.Model}
	for iter := 0; iter < cfg.MaxIterations; iter++ {
		trace.Iters = iter + 1
		reply, err := a.llm.Send(ctx, LLMRequest{
			System:    sys,
			Turns:     user,
			Tools:     toolDefs,
			Model:     cfg.Model,
			MaxTokens: cfg.MaxTokens,
		})
		if err != nil {
			if ctx.Err() != nil {
				return fallback(cfg.OnTimeout, "agent timed out", trace)
			}
			return fallback(cfg.OnError, "agent error: "+err.Error(), trace)
		}
		if reply.Tokens != nil {
			if trace.Tokens == nil {
				trace.Tokens = &eventlog.TokenCounts{}
			}
			trace.Tokens.Input += reply.Tokens.Input
			trace.Tokens.Output += reply.Tokens.Output
		}

		// Append the assistant turn verbatim so the next request sees it.
		user = append(user, Turn{Role: "assistant", Blocks: reply.Blocks})

		// Did the model issue tool calls?
		var toolUses []Block
		var assistantText []string
		for _, b := range reply.Blocks {
			switch b.Type {
			case BlockToolUse:
				toolUses = append(toolUses, b)
			case BlockText:
				assistantText = append(assistantText, b.Text)
			}
		}

		if len(toolUses) == 0 {
			final := strings.TrimSpace(strings.Join(assistantText, "\n"))
			trace.Final = final
			d := parseDecision(final)
			d.Trace = trace
			return d
		}

		// Run each tool sequentially. Build a single user turn carrying
		// all the tool_result blocks. (Anthropic accepts multiple
		// tool_result blocks in one user message.)
		var resultBlocks []Block
		for _, tu := range toolUses {
			out, err := executeTool(ctx, cfg.Tools, tu.ToolName, tu.ToolInput)
			tc := eventlog.AgentTool{Name: tu.ToolName, Input: tu.ToolInput}
			if err != nil {
				tc.Err = err.Error()
				trace.ToolCalls = append(trace.ToolCalls, tc)
				resultBlocks = append(resultBlocks, Block{
					Type: BlockToolResult, ToolUseID: tu.ToolUseID,
					Text: "ERROR: " + err.Error(), IsError: true,
				})
				continue
			}
			tc.Output = string(out)
			trace.ToolCalls = append(trace.ToolCalls, tc)
			resultBlocks = append(resultBlocks, Block{
				Type: BlockToolResult, ToolUseID: tu.ToolUseID, Text: string(out),
			})
		}
		user = append(user, Turn{Role: "user", Blocks: resultBlocks})
	}
	// Out of iterations.
	return fallback(cfg.OnTimeout, "agent exhausted max iterations", trace)
}

func executeTool(ctx context.Context, reg *tools.Registry, name string, input json.RawMessage) (json.RawMessage, error) {
	if reg == nil {
		return nil, fmt.Errorf("no tool registry configured")
	}
	return reg.Execute(ctx, name, input)
}

func fallback(policy, reason string, trace *eventlog.AgentTrace) Decision {
	d := Decision{Trace: trace, Reason: reason}
	switch policy {
	case "deny":
		d.Outcome = "deny"
	case "escalate":
		d.Outcome = "ask"
		d.Question = reason
	default: // "approve" — the documented fail-open default
		d.Outcome = "approve"
	}
	return d
}

// parseDecision pulls the trailing ARBITER_DECISION line out of the
// assistant text. Tolerant: accepts decision on its own line, accepts a
// leading dash or em-dash as the reason separator, and accepts simple
// JSON ({"decision":"deny","reason":"..."}) as a fallback.
func parseDecision(text string) Decision {
	if dec, ok := tryParseJSON(text); ok {
		return dec
	}
	for _, line := range reverseLines(text) {
		l := strings.TrimSpace(line)
		const prefix = "ARBITER_DECISION:"
		idx := strings.Index(l, prefix)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(l[idx+len(prefix):])
		// rest = "approve" | "deny — reason" | "ask — question"
		outcome, reason := splitOutcomeReason(rest)
		switch outcome {
		case "approve":
			return Decision{Outcome: "approve", Reason: reason}
		case "deny":
			return Decision{Outcome: "deny", Reason: reason}
		case "ask":
			return Decision{Outcome: "ask", Question: reason}
		}
	}
	// No valid marker: treat as malformed → deny with the body as reason.
	return Decision{Outcome: "deny", Reason: "agent did not emit ARBITER_DECISION line; refusing"}
}

func splitOutcomeReason(s string) (outcome, reason string) {
	for _, sep := range []string{" — ", " - ", ": "} {
		if i := strings.Index(s, sep); i > 0 {
			return strings.ToLower(strings.TrimSpace(s[:i])), strings.TrimSpace(s[i+len(sep):])
		}
	}
	return strings.ToLower(strings.TrimSpace(s)), ""
}

func tryParseJSON(text string) (Decision, bool) {
	t := strings.TrimSpace(text)
	if !(strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")) {
		return Decision{}, false
	}
	var raw struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal([]byte(t), &raw); err != nil {
		return Decision{}, false
	}
	switch strings.ToLower(raw.Decision) {
	case "approve", "allow":
		return Decision{Outcome: "approve", Reason: raw.Reason}, true
	case "deny", "block":
		return Decision{Outcome: "deny", Reason: raw.Reason}, true
	case "ask", "escalate":
		q := raw.Question
		if q == "" {
			q = raw.Reason
		}
		return Decision{Outcome: "ask", Question: q}, true
	}
	return Decision{}, false
}

func reverseLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, len(lines))
	for i, l := range lines {
		out[len(lines)-1-i] = l
	}
	return out
}

func buildSystemPrompt(cfg Config, a Action) string {
	var b strings.Builder
	b.WriteString("You are godx-arbiter, a decision agent for an AI coding CLI.\n")
	b.WriteString("Your job: decide whether the proposed tool call should be approved, denied, or escalated to the human user.\n")
	b.WriteString("Use the available tools to gather context (analyze_risk, check_rule, lookup_history, read_file) before deciding.\n")
	b.WriteString("Be concise. End with exactly one line:\n")
	b.WriteString("  ARBITER_DECISION: approve\n")
	b.WriteString("  ARBITER_DECISION: deny — <one-sentence reason shown to the calling agent>\n")
	b.WriteString("  ARBITER_DECISION: ask — <question for the human>\n\n")

	if cfg.RulesBody != "" {
		b.WriteString("--- PROJECT rules.md ---\n")
		b.WriteString(cfg.RulesBody)
		if !strings.HasSuffix(cfg.RulesBody, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("--- end rules.md ---\n\n")
	}
	for _, skill := range cfg.SkillTexts {
		b.WriteString("--- SKILL ---\n")
		b.WriteString(skill)
		if !strings.HasSuffix(skill, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("--- end skill ---\n\n")
	}

	b.WriteString("--- ACTION UNDER REVIEW ---\n")
	fmt.Fprintf(&b, "tool:    %s\n", a.ToolName)
	if a.Cwd != "" {
		fmt.Fprintf(&b, "cwd:     %s\n", a.Cwd)
	}
	if a.Project != "" {
		fmt.Fprintf(&b, "project: %s\n", a.Project)
	}
	if a.SessionID != "" {
		fmt.Fprintf(&b, "session: %s\n", a.SessionID)
	}
	if len(a.ToolInput) > 0 {
		fmt.Fprintf(&b, "input:   %s\n", string(a.ToolInput))
	}
	b.WriteString("--- end action ---\n")
	return b.String()
}

func userInstruction() string {
	return "Decide. Use tools as needed. End with exactly one ARBITER_DECISION line."
}

// buildToolDefs converts an arbiter tool registry into the agent's
// LLM-facing tool descriptors.
func buildToolDefs(reg *tools.Registry) []ToolDef {
	if reg == nil {
		return nil
	}
	var defs []ToolDef
	for _, t := range reg.All() {
		defs = append(defs, ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}
