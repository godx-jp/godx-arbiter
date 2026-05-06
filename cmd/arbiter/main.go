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
	"errors"
	"fmt"
	"os"

	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/hookio"
	"github.com/godx-team/godx-arbiter/internal/projectfind"
)

// version is overridden at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
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
		usage(os.Stdout)
	case "init", "mcp", "proxy", "usage", "explain":
		fmt.Fprintf(os.Stderr, "%s: not implemented yet (see docs/ROADMAP.md)\n", cmd)
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
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
// Through Step 2: parse stdin, detect project, load config, return
// approve with metadata. The fast-path policy engine and the slow-path
// agent (Steps 3 and 4) plug in after this scaffolding.
func hookPreTool() {
	defer failOpen("pretool")

	in, err := hookio.ReadInput(os.Stdin)
	if err != nil {
		// Fail open per ADR-005 (default on_error: approve).
		_ = hookio.WriteAllow(os.Stdout, "")
		fmt.Fprintf(os.Stderr, "[arbiter] pretool: read input: %v\n", err)
		return
	}

	meta := map[string]any{
		"step":       "2-stub",
		"tool":       in.ToolName,
		"session_id": in.SessionID,
	}

	// Project detection. cwd may be empty for non-Claude-Code callers
	// in proxy mode (Step 11+); try cwd then process cwd as fallback.
	cwd := in.Cwd
	if cwd == "" {
		if c, _ := os.Getwd(); c != "" {
			cwd = c
		}
	}
	if cwd != "" {
		switch proj, err := config.LoadFromCwd(cwd); {
		case err == nil:
			meta["project_root"] = proj.Root
			meta["has_rules"] = proj.HasRules()
			meta["has_policy"] = proj.HasPolicy()
			if proj.HasPolicy() {
				meta["policy_rule_count"] = len(proj.Policy.Allow) + len(proj.Policy.Deny) + len(proj.Policy.ToAgent)
			}
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

	_ = hookio.WriteAllowWithMeta(os.Stdout, "", meta)
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
