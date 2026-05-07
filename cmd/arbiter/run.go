package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/godx-team/godx-arbiter/internal/auth"
	"github.com/godx-team/godx-arbiter/internal/notify"
	"github.com/godx-team/godx-arbiter/internal/runner"
)

// runRun dispatches `arbiter run`. See docs/CLI.md#arbiter-run.
//
// Auto-orchestrates a Claude Code (or codex / gemini / antigravity)
// session for one task: spawns the CLI headless, streams the
// stream-json events to the user's terminal, records a per-run JSONL
// log + eventlog start/end rows, and exits with a documented status
// code (0 / 1 / 2 / 124 / 130).
//
// Trade-offs are spelled out in docs/RUN.md. The two big ones:
//
//   - No tmux at v0.1. Stream-json's message_stop event is the
//     unambiguous completion signal; tmux + send-keys would only
//     reintroduce race conditions for no transport benefit.
//   - Foreground only at v0.1. Ctrl-C cancels. Detach/reattach is a
//     v0.2 follow-up where tmux genuinely earns its keep.
func runRun(args []string) {
	if len(args) > 0 && (args[0] == "--list" || args[0] == "-l") {
		runRunList(args[1:])
		return
	}
	if len(args) > 0 && args[0] == "--resume-last" {
		args = append([]string{"--continue"}, args[1:]...)
	}

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cliName := fs.String("cli", "claude", "CLI to spawn: claude|codex|gemini|antigravity")
	cwd := fs.String("cwd", "", "Working directory pinned for the child (default: $PWD)")
	timeout := fs.Duration("timeout", 30*time.Minute, "Hard wall-clock cap for the run")
	output := fs.String("output", "stream", "Output mode: stream|final|json")
	logFile := fs.String("log-file", "", "Override the default per-run log path")
	id := fs.String("id", "", "Run id (default: auto-generated, also used as session_id in eventlog)")
	quiet := fs.Bool("quiet", false, "Suppress live render; log file is still written")
	notifyOnDone := fs.Bool("notify-on-done", false, "Trigger the configured notify channel on finish")
	taskFile := fs.String("task-file", "", "Read the prompt from a file instead of argv")
	taskStdin := fs.Bool("task-stdin", false, "Read the prompt from stdin")

	// Fidelity passthrough — fixes the 'session hời hợt' concern.
	resume := fs.String("resume", "", "Pass-through to claude --resume <id>")
	cont := fs.Bool("continue", false, "Pass-through to claude --continue (resume most recent)")
	allowedTools := fs.String("allowed-tools", "", "Comma-list of tools allowed (claude --allowedTools)")
	deniedTools := fs.String("denied-tools", "", "Comma-list of tools denied (claude --disallowedTools)")
	permissionMode := fs.String("permission-mode", "", "claude --permission-mode (default|plan|acceptEdits)")
	mcpConfig := fs.String("mcp-config", "", "Extra MCP server config file (claude --mcp-config)")
	addDirsFlag := fs.String("add-dirs", "", "Comma-list of extra directories the child may read (claude --add-dir)")
	model := fs.String("model", "", "Pin the model (claude --model). Empty = claude chooses.")

	// Safety.
	unsafeSkip := fs.Bool("unsafe-skip-permissions", false, "Pass --dangerously-skip-permissions; refused unless GODX_ARBITER_ALLOW_UNSAFE=1")
	noHooks := fs.Bool("no-arbiter-hooks", false, "Disable arbiter's own hooks for the spawned session (dev iteration only)")
	inheritEnv := fs.Bool("inherit-env", false, "Pass the caller's full env instead of the curated allowlist")
	forceResume := fs.Bool("force-resume", false, "Resume a session even if its original cwd differs from the current cwd")
	keepOnHup := fs.Bool("keep-running", false, "Ignore SIGHUP so closing the terminal / SSH dropout doesn't kill the run (output continues to log file)")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	task, err := readTask(fs.Args(), *taskFile, *taskStdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(2)
	}

	spec := runner.RunSpec{
		CLI:                   runner.CLI(*cliName),
		Task:                  task,
		Cwd:                   *cwd,
		Timeout:               *timeout,
		OutputMode:            runner.OutputMode(*output),
		LogPath:               *logFile,
		ID:                    *id,
		Quiet:                 *quiet,
		NotifyOnDone:          *notifyOnDone,
		Resume:                *resume,
		Continue:              *cont,
		AllowedTools:          splitCSVFlag(*allowedTools),
		DeniedTools:           splitCSVFlag(*deniedTools),
		PermissionMode:        runner.PermissionMode(*permissionMode),
		MCPConfig:             *mcpConfig,
		AddDirs:               splitCSVFlag(*addDirsFlag),
		Model:                 *model,
		UnsafeSkipPermissions: *unsafeSkip,
		NoArbiterHooks:        *noHooks,
		InheritEnv:            *inheritEnv,
	}

	if spec.Cwd == "" {
		c, _ := os.Getwd()
		spec.Cwd = c
	}

	// Refuse a stray --resume from a different project before the
	// runner spawns anything — saves a flaky "wait, why is the agent
	// looking at the wrong CLAUDE.md?" debugging session.
	if err := runner.VerifyResumeCwd(spec.Resume, spec.Cwd, *forceResume); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(2)
	}

	if spec.UnsafeSkipPermissions {
		fmt.Fprintln(os.Stderr, "\x1b[31m⚠ --unsafe-skip-permissions: child runs with --dangerously-skip-permissions\x1b[0m")
	}

	// Pre-flight: warn loudly if Anthropic auth isn't reachable when
	// CLI is claude. We don't refuse — the user might have run
	// `claude /login` and have an OAuth token claude itself manages.
	if spec.CLI == runner.CLIClaude || spec.CLI == "" {
		if v, _ := auth.Get(auth.ProviderAnthropic); v == "" {
			fmt.Fprintln(os.Stderr, "[arbiter run] note: no ANTHROPIC_API_KEY in env or keychain; claude will rely on its own auth (`claude /login`)")
		}
	}

	if *keepOnHup {
		// Ignore SIGHUP so a terminal close / SSH dropout doesn't tear
		// the run down. signal.Ignore detaches the default action;
		// SIGHUP is delivered to the process group, but with the
		// handler ignored arbiter (and thus its child) survive.
		signal.Ignore(syscall.SIGHUP)
		fmt.Fprintln(os.Stderr, "[arbiter run] SIGHUP ignored — terminal close won't kill this run")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if spec.Timeout > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, spec.Timeout)
		defer stop()
	}

	r := runner.New()
	result, err := r.Run(ctx, spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[arbiter run]", err)
		os.Exit(1)
	}

	if result.Reason != "" && result.Outcome != runner.OutcomeOK {
		fmt.Fprintf(os.Stderr, "\n[arbiter run] %s: %s\n", result.Outcome, result.Reason)
	}

	// Resume hint: when a run is interrupted / timed out / failed
	// mid-stream, point the user at the recovery path. Use result.ID
	// (resolved) rather than spec.ID (may be empty pre-resolution).
	switch result.Outcome {
	case runner.OutcomeInterrupted, runner.OutcomeTimeout, runner.OutcomeChildFailed:
		if result.ID != "" {
			fmt.Fprintf(os.Stderr, "[arbiter run] to resume: arbiter run --resume %s -- \"<continuation prompt>\"\n", result.ID)
		}
	}

	if spec.NotifyOnDone {
		dispatchNotify(spec, result)
	}

	// Summary always prints (--quiet suppresses live rendering, not the
	// final outcome line — users still need to know pass/fail).
	fmt.Fprintf(os.Stderr, "\n[arbiter run] %s in %s — exit=%d log=%s\n",
		result.Outcome, time.Duration(result.DurationMs)*time.Millisecond, result.ExitCode, result.LogPath)

	os.Exit(result.ExitCode)
}

// runRunList prints a friendly summary of recent runs (`arbiter run --list`).
func runRunList(args []string) {
	fs := flag.NewFlagSet("run --list", flag.ExitOnError)
	limit := fs.Int("n", 20, "Number of runs to show")
	jsonOut := fs.Bool("json", false, "Emit raw index entries as JSONL")
	_ = fs.Parse(args)

	entries, err := runner.ListRecent(*limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run --list:", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("(no runs recorded yet)")
		return
	}
	if *jsonOut {
		for _, e := range entries {
			fmt.Println(jsonOneLine(e))
		}
		return
	}
	fmt.Printf("%-26s %-10s %-9s %-30s %-30s\n", "ID", "CLI", "OUTCOME", "STARTED", "CWD")
	for _, e := range entries {
		started := e.Started.Local().Format("2006-01-02 15:04:05")
		outcome := string(e.Outcome)
		if outcome == "" {
			outcome = "(in flight)"
		}
		cwd := e.Cwd
		if len(cwd) > 30 {
			cwd = "…" + cwd[len(cwd)-29:]
		}
		fmt.Printf("%-26s %-10s %-9s %-30s %-30s\n", e.ID, e.CLI, outcome, started, cwd)
	}
}

func dispatchNotify(spec runner.RunSpec, result *runner.RunResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	question := fmt.Sprintf("arbiter run %s — %s", spec.ID, result.Outcome)
	if result.Reason != "" {
		question += ": " + result.Reason
	}
	_, _ = notify.Default.Dispatch(ctx, notify.EscalateRequest{
		SessionID:   spec.ID,
		ProjectRoot: spec.Cwd,
		Question:    question,
		Channels:    []string{"telegram", "desktop", "log"},
		Context: map[string]any{
			"cli":       spec.CLI,
			"exit":      result.ExitCode,
			"duration":  fmt.Sprintf("%dms", result.DurationMs),
			"final_tok": result.OutputTok,
		},
		Timeout: 5 * time.Second,
	})
}

func readTask(positional []string, taskFile string, taskStdin bool) (string, error) {
	if taskFile != "" {
		raw, err := os.ReadFile(taskFile)
		if err != nil {
			return "", fmt.Errorf("read --task-file: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	if taskStdin {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	if len(positional) == 0 {
		return "", fmt.Errorf("task prompt required (positional, --task-file, or --task-stdin)")
	}
	return strings.Join(positional, " "), nil
}

func splitCSVFlag(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// jsonOneLine is a tiny helper to avoid pulling encoding/json into
// run.go's import group when only --list --json needs it.
func jsonOneLine(v any) string {
	out, err := jsonMarshalIndent(v)
	if err != nil {
		return fmt.Sprintf("// json error: %v", err)
	}
	// jsonMarshalIndent returns indented; flatten for one-line output.
	return strings.Join(strings.Fields(out), " ")
}
