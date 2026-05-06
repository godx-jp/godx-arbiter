// Command arbiter is the godx-arbiter CLI entrypoint.
//
// Subcommands:
//
//	arbiter hook <pretool|notification|stop|posttool>   Hook entrypoint (reads stdin)
//	arbiter init                                        Set up hooks + .arbiter/ in cwd
//	arbiter doctor                                      Diagnose install + config
//	arbiter mcp                                         Run MCP stdio server
//	arbiter proxy                                       Run LLM proxy server
//	arbiter usage                                       Token + cost report
//	arbiter explain <session-id>                        Replay past decisions
//	arbiter version                                     Print version
//
// Step 1 of the roadmap implements only the CLI shim and hook
// stdin/stdout plumbing. Real decision logic lands in steps 2-4.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/godx-team/godx-arbiter/internal/agent"
	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/eventlog"
	"github.com/godx-team/godx-arbiter/internal/hookio"
	"github.com/godx-team/godx-arbiter/internal/notify"
	"github.com/godx-team/godx-arbiter/internal/policy"
	"github.com/godx-team/godx-arbiter/internal/projectfind"
	"github.com/godx-team/godx-arbiter/internal/skills"
	"github.com/godx-team/godx-arbiter/internal/tools"
)

// version is overridden at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "hook":
		runHook(args)
	case "doctor":
		runDoctor(args)
	case "version", "--version", "-v":
		fmt.Println("godx-arbiter", version)
	case "help", "--help", "-h":
		printUsage(os.Stdout)
	case "init":
		runInit(args)
	case "mcp":
		runMCP(args)
	case "proxy":
		runProxy(args)
	case "usage":
		runUsage(args)
	case "explain":
		runExplain(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	fmt.Fprint(w, `godx-arbiter — LLM-based decision arbiter for AI coding CLIs

Usage: arbiter <command> [args...]

Commands:
  hook <pretool|notification|stop|posttool>
                        Hook entrypoint, reads JSON on stdin, writes
                        decision JSON on stdout.
  init                  Set up hooks in ~/.claude/settings.json and
                        scaffold .arbiter/ in cwd.
  doctor                Diagnose install + config.
  mcp                   Run MCP stdio server (decision-support tools).
  proxy                 Run local LLM proxy for non-Claude CLIs.
  usage                 Token + cost report.
  explain <session-id>  Replay past decisions with full rationale.
  version               Print version.
  help                  This message.
`)
}

// runHook dispatches the hook subcommand. The cardinal rule (ADR-005):
// arbiter must never break a calling session — fail-open on errors.
func runHook(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "hook: subcommand required (pretool|notification|stop|posttool)")
		os.Exit(2)
	}
	switch args[0] {
	case "pretool":
		hookPreTool()
	case "notification":
		hookNotification()
	case "stop":
		hookStop()
	case "posttool":
		hookPostTool()
	default:
		fmt.Fprintf(os.Stderr, "unknown hook: %s\n", args[0])
		os.Exit(2)
	}
}

// hookPreTool is the PreToolUse entrypoint.
//
// Steps 1–3: parse stdin, detect project, load config, evaluate the
// fast-path policy, and emit an allow/deny decision when the regex
// engine has a verdict. Slow-path (Step 4) takes over for OutcomeAgent.
func hookPreTool() {
	defer failOpen("pretool")
	start := time.Now()

	in, err := hookio.ReadInput(os.Stdin)
	if err != nil {
		// Fail open per ADR-005 (default on_error: approve).
		_ = hookio.WriteAllow(os.Stdout, "")
		fmt.Fprintf(os.Stderr, "[arbiter] pretool: read input: %v\n", err)
		return
	}

	if os.Getenv("GODX_ARBITER_DISABLED") == "1" {
		_ = hookio.WriteAllowWithMeta(os.Stdout, "arbiter disabled via env", map[string]any{
			"path": "kill-switch",
			"tool": in.ToolName,
		})
		return
	}

	meta := map[string]any{
		"tool":       in.ToolName,
		"session_id": in.SessionID,
		"path":       "fast-path",
	}

	// Project detection. cwd may be empty for non-Claude-Code callers
	// in proxy mode (Step 11+); try cwd then process cwd as fallback.
	cwd := in.Cwd
	if cwd == "" {
		if c, _ := os.Getwd(); c != "" {
			cwd = c
		}
	}

	var proj *config.Project
	if cwd != "" {
		switch p, err := config.LoadFromCwd(cwd); {
		case err == nil:
			proj = p
			meta["project_root"] = proj.Root
			meta["has_rules"] = proj.HasRules()
			meta["has_policy"] = proj.HasPolicy()
			if !proj.IsConfigured() {
				meta["note"] = "no rules.md or policy.yaml — using built-in defaults"
			}
		case errors.Is(err, projectfind.ErrNotFound):
			meta["project_root"] = ""
			meta["note"] = "no .arbiter/ in cwd or any ancestor — built-in defaults"
		default:
			// Hard parse error somewhere in the project's config. Log
			// to stderr but still fail-open (ADR-005).
			fmt.Fprintf(os.Stderr, "[arbiter] pretool: project config: %v\n", err)
			meta["config_error"] = err.Error()
		}
	}

	// Front-matter kill switch: rules.md `enabled: false` → approve all.
	if proj.HasRules() && !proj.Rules.IsEnabled() {
		meta["path"] = "kill-switch"
		meta["note"] = "rules.md enabled=false"
		meta["duration_ms"] = msSince(start)
		_ = hookio.WriteAllowWithMeta(os.Stdout, "rules.md kill switch", meta)
		return
	}

	var pol *config.Policy
	if proj.HasPolicy() {
		pol = proj.Policy
	}
	d := policy.Eval(pol, &policy.Action{
		ToolName:  in.ToolName,
		ToolInput: in.ToolInput,
	})
	meta["rule_type"] = d.RuleType
	meta["rule_index"] = d.RuleIndex
	if d.Pattern != "" {
		meta["matched_pattern"] = d.Pattern
	}

	switch d.Outcome {
	case policy.OutcomeDeny:
		meta["duration_ms"] = msSince(start)
		reason := deriveReason(d, "denied by fast-path policy")
		_ = hookio.WriteDenyWithMeta(os.Stdout, reason, meta)
		logEvent(in, proj, "fast-path", "deny", reason, msSince(start), nil)
	case policy.OutcomeAllow:
		meta["duration_ms"] = msSince(start)
		reason := deriveReason(d, "")
		_ = hookio.WriteAllowWithMeta(os.Stdout, reason, meta)
		logEvent(in, proj, "fast-path", "allow", reason, msSince(start), nil)
	default: // OutcomeAgent
		runSlowPath(in, proj, meta, start)
	}
}

// runSlowPath spawns the LLM agent. If no API key is configured (or
// initialization otherwise fails), the rules.md `on_error` policy
// decides — default approve, per ADR-005.
func runSlowPath(in *hookio.Input, proj *config.Project, meta map[string]any, start time.Time) {
	cfg := agent.DefaultConfig(rulesOf(proj))
	cfg.Tools = tools.DefaultRegistry()
	cfg.SkillTexts = loadSkills(proj)

	llm, err := agent.NewAnthropicLLM()
	if err != nil {
		meta["path"] = "agent-stub"
		meta["note"] = "no ANTHROPIC_API_KEY — slow-path skipped (on_error: " + cfg.OnError + ")"
		meta["duration_ms"] = msSince(start)
		emitFallback(in, proj, cfg.OnError, meta, start, "agent unavailable: no ANTHROPIC_API_KEY")
		return
	}

	a := agent.New(llm)
	ctx := context.Background()
	d := a.Decide(ctx, cfg, agent.Action{
		SessionID: in.SessionID,
		Project:   projectRoot(proj),
		Cwd:       in.Cwd,
		ToolName:  in.ToolName,
		ToolInput: in.ToolInput,
	})
	meta["path"] = "slow-path"
	meta["agent_outcome"] = d.Outcome
	meta["duration_ms"] = msSince(start)
	if d.Trace != nil {
		meta["agent_iters"] = d.Trace.Iters
	}

	switch d.Outcome {
	case "approve":
		_ = hookio.WriteAllowWithMeta(os.Stdout, d.Reason, meta)
		logEvent(in, proj, "slow-path", "allow", d.Reason, msSince(start), d.Trace)
	case "deny":
		_ = hookio.WriteDenyWithMeta(os.Stdout, d.Reason, meta)
		logEvent(in, proj, "slow-path", "deny", d.Reason, msSince(start), d.Trace)
	case "ask":
		// Try escalation. If notify isn't configured, fall back per
		// rules.md on_timeout policy (default deny).
		ctx2, cancel := context.WithTimeout(ctx, escalationTimeout(rulesOf(proj)))
		defer cancel()
		reply, err := notify.Escalate(ctx2, notify.EscalateRequest{
			Channels:    notifyChannels(rulesOf(proj)),
			Question:    d.Question,
			Context:     escalationContext(in, d),
			ProjectRoot: projectRoot(proj),
			SessionID:   in.SessionID,
		})
		if err != nil || reply.Timeout {
			fallbackOutcome := timeoutFallback(rulesOf(proj))
			meta["escalation"] = "timeout"
			meta["fallback"] = fallbackOutcome
			emitFallback(in, proj, fallbackOutcome, meta, start, "escalation timed out: "+d.Question)
			return
		}
		meta["escalation"] = reply.Channel
		switch strings.ToLower(reply.Reply) {
		case "approve", "allow":
			_ = hookio.WriteAllowWithMeta(os.Stdout, "user approved via "+reply.Channel, meta)
			logEvent(in, proj, "escalation", "allow", "user approved", msSince(start), d.Trace)
		default:
			_ = hookio.WriteDenyWithMeta(os.Stdout, "user denied via "+reply.Channel, meta)
			logEvent(in, proj, "escalation", "deny", "user denied", msSince(start), d.Trace)
		}
	default:
		emitFallback(in, proj, cfg.OnError, meta, start, "unknown agent outcome: "+d.Outcome)
	}
}

func emitFallback(in *hookio.Input, proj *config.Project, policy string, meta map[string]any, start time.Time, reason string) {
	switch policy {
	case "deny":
		_ = hookio.WriteDenyWithMeta(os.Stdout, reason, meta)
		logEvent(in, proj, "fallback", "deny", reason, msSince(start), nil)
	default:
		_ = hookio.WriteAllowWithMeta(os.Stdout, reason, meta)
		logEvent(in, proj, "fallback", "allow", reason, msSince(start), nil)
	}
}

func rulesOf(proj *config.Project) *config.Rules {
	if proj == nil {
		return nil
	}
	return proj.Rules
}

func projectRoot(proj *config.Project) string {
	if proj == nil {
		return ""
	}
	return proj.Root
}

func loadSkills(proj *config.Project) []string {
	if proj == nil || proj.Rules == nil {
		return nil
	}
	root := proj.Root
	bodies, err := skills.Resolve(root, proj.Rules.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[arbiter] skills: %v\n", err)
		return nil
	}
	return bodies
}

func escalationTimeout(r *config.Rules) time.Duration {
	if r != nil && r.FrontMatter.EscalationTimeoutSeconds > 0 {
		return time.Duration(r.FrontMatter.EscalationTimeoutSeconds) * time.Second
	}
	return 60 * time.Second
}

func notifyChannels(r *config.Rules) []string {
	if r == nil || len(r.FrontMatter.NotifyChannels) == 0 {
		return []string{"desktop"}
	}
	return r.FrontMatter.NotifyChannels
}

func timeoutFallback(r *config.Rules) string {
	if r == nil || r.FrontMatter.OnTimeout == "" {
		return "deny"
	}
	return r.FrontMatter.OnTimeout
}

func escalationContext(in *hookio.Input, d agent.Decision) map[string]any {
	ctx := map[string]any{
		"tool": in.ToolName,
	}
	if len(in.ToolInput) > 0 {
		ctx["input"] = string(in.ToolInput)
	}
	if d.Reason != "" {
		ctx["agent_reason"] = d.Reason
	}
	return ctx
}

func logEvent(in *hookio.Input, proj *config.Project, path, decision, reason string, ms int64, trace *eventlog.AgentTrace) {
	ev := eventlog.Event{
		SessionID:  in.SessionID,
		Project:    projectRoot(proj),
		Tool:       in.ToolName,
		InputSum:   summarize(in.ToolInput),
		Path:       path,
		Decision:   decision,
		Reason:     reason,
		DurationMs: ms,
		Agent:      trace,
	}
	if err := eventlog.Append(ev); err != nil {
		fmt.Fprintf(os.Stderr, "[arbiter] eventlog append: %v\n", err)
	}
}

func summarize(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	const max = 240
	s := string(raw)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// deriveReason picks a human-readable reason: prefer the rule's own
// reason text; fall back to the supplied default.
func deriveReason(d *policy.Decision, fallback string) string {
	if d == nil {
		return fallback
	}
	if d.Reason != "" {
		return d.Reason
	}
	return fallback
}

func msSince(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}

// hookNotification is fired when Claude needs user attention. Step 1:
// no-op (drain stdin so the writer doesn't get EPIPE).
func hookNotification() {
	defer failOpenSilent("notification")
	_, _ = hookio.ReadInput(os.Stdin)
}

// hookStop is fired when a session ends. Step 1: no-op.
func hookStop() {
	defer failOpenSilent("stop")
	_, _ = hookio.ReadInput(os.Stdin)
}

// hookPostTool is fired after a tool completes. Step 1: no-op.
// In step 4 this writes to the eventlog for lookup_history.
func hookPostTool() {
	defer failOpenSilent("posttool")
	_, _ = hookio.ReadInput(os.Stdin)
}

// failOpen recovers from any panic in a hook handler and emits an
// approve decision so the calling session is never broken by a bug
// in arbiter. Logs the panic to stderr for diagnosis.
func failOpen(hook string) {
	if r := recover(); r != nil {
		_ = hookio.WriteAllow(os.Stdout, "")
		fmt.Fprintf(os.Stderr, "[arbiter] %s: panic recovered: %v\n", hook, r)
	}
}

// failOpenSilent is like failOpen but does not write to stdout
// (for hooks that don't expect a decision response).
func failOpenSilent(hook string) {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "[arbiter] %s: panic recovered: %v\n", hook, r)
	}
}
