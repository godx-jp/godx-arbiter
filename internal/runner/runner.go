package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/godx-team/godx-arbiter/internal/eventlog"
)

// Runner is the spawn engine. The function fields are seams for
// tests — production callers don't override them.
type Runner struct {
	// LookPath resolves the child binary on $PATH. Default exec.LookPath.
	LookPath func(name string) (string, error)

	// Now returns the current time; tests use it to freeze timestamps.
	Now func() time.Time
}

// New constructs a Runner with production defaults.
func New() *Runner {
	return &Runner{LookPath: exec.LookPath, Now: time.Now}
}

// Run spawns the child described by spec and pumps its stream-json
// output through the chosen renderer + a per-run log file. Returns
// when the child exits, ctx is cancelled, or the renderer signals a
// hard error.
//
// The cardinal rule (ADR-005 spirit applied to orchestration):
// arbiter must not crash on the caller and must not orphan a child
// process under any failure mode. Three layers of protection:
//
//   - context cancel triggers cmd.Cancel which kills the process
//     group (the normal path);
//   - the deferred recover() below catches panics in the runner code
//     itself, kills the process group via spawnedCmd, and surfaces
//     the panic as OutcomeChildFailed;
//   - sysProcAttr() sets PR_SET_PDEATHSIG=SIGTERM on Linux so even
//     SIGKILL of arbiter (OOM, kill -9) leaves no orphan claude.
func (r *Runner) Run(ctx context.Context, spec RunSpec) (resultOut *RunResult, errOut error) {
	r = withDefaults(r)
	resolved, err := resolveSpec(r, spec)
	if err != nil {
		return nil, err
	}
	spec = resolved

	// Panic shield: any bug in the runner code below must not orphan
	// the child. spawnedCmd is captured at exec.Cmd build time so the
	// recovery path can kill its process group even if the panic
	// happened before normal cleanup.
	var spawnedCmd *exec.Cmd
	defer func() {
		if rec := recover(); rec != nil {
			if spawnedCmd != nil {
				_ = killGroup(spawnedCmd)
			}
			fmt.Fprintf(spec.Stderr, "[arbiter run] panic recovered: %v\n", rec)
			resultOut = finishRefused(r, spec, OutcomeChildFailed,
				fmt.Sprintf("runner panic: %v", rec))
		}
	}()

	if spec.UnsafeSkipPermissions {
		if err := guardUnsafe(spec); err != nil {
			return finishRefused(r, spec, OutcomeRefused, err.Error()), nil
		}
	}

	// Recursion fuse: refuse if already several levels deep. Each call
	// bumps the env counter so child claude → arbiter hook → arbiter
	// run → … can't fork-bomb.
	depth := atoiOr(os.Getenv("ARBITER_RUN_DEPTH"), 0)
	if depth > 2 {
		return finishRefused(r, spec, OutcomeRefused, fmt.Sprintf("ARBITER_RUN_DEPTH=%d (max 2) — refusing to recurse further", depth)), nil
	}

	inv, err := BuildInvocation(spec)
	if err != nil {
		return finishRefused(r, spec, OutcomeRefused, err.Error()), nil
	}
	abs, err := r.LookPath(inv.Bin)
	if err != nil {
		return finishRefused(r, spec, OutcomeRefused, fmt.Sprintf("%s not on PATH (%v)", inv.Bin, err)), nil
	}
	if filepath.Base(abs) != inv.Bin && filepath.Base(abs) != inv.Bin+".exe" {
		return finishRefused(r, spec, OutcomeRefused, fmt.Sprintf("resolved %s to %s — basename mismatch, refusing", inv.Bin, abs)), nil
	}

	logFile, err := os.Create(spec.LogPath)
	if err != nil {
		return finishRefused(r, spec, OutcomeRefused, fmt.Sprintf("create log %s: %v", spec.LogPath, err)), nil
	}
	defer logFile.Close()

	startedAt := r.Now()
	startEntry := IndexEntry{
		ID:      spec.ID,
		CLI:     spec.CLI,
		Model:   spec.Model,
		Cwd:     spec.Cwd,
		Started: startedAt,
		LogPath: spec.LogPath,
	}
	_ = AppendIndex(startEntry)
	_ = eventlog.Append(eventlog.Event{
		TS:        startedAt,
		EventID:   spec.ID,
		SessionID: spec.ID,
		Project:   spec.Cwd,
		Tool:      string(spec.CLI),
		InputSum:  truncate(spec.Task, 240),
		Path:      "run",
		Decision:  "spawned",
	})

	cmd := exec.CommandContext(ctx, abs, inv.Args...)
	cmd.Dir = spec.Cwd
	cmd.Env = buildEnv(spec, depth)
	cmd.Stdin = strings.NewReader(inv.Stdin)
	cmd.Stderr = spec.Stderr
	cmd.SysProcAttr = sysProcAttr()
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second
	spawnedCmd = cmd // for the panic shield deferred at function entry

	result := &RunResult{
		ID:        spec.ID,
		CLI:       spec.CLI,
		Cwd:       spec.Cwd,
		StartedAt: startedAt,
		LogPath:   spec.LogPath,
	}

	usesStreamJSON := spec.CLI == CLIClaude || spec.CLI == ""
	gotStop := false
	renderer := chooseRenderer(spec, spec.Stdout)

	if usesStreamJSON {
		stdoutR, stdoutW := io.Pipe()
		defer stdoutR.Close()
		cmd.Stdout = io.MultiWriter(stdoutW, logFile)

		if err := cmd.Start(); err != nil {
			stdoutW.Close()
			return finishRefused(r, spec, OutcomeRefused, fmt.Sprintf("spawn %s: %v", abs, err)), nil
		}

		events, decodeErrs := DecodeStream(stdoutR, func(err error) {
			fmt.Fprintf(spec.Stderr, "[arbiter run] %v\n", err)
		})

		go func() {
			_ = cmd.Wait()
			stdoutW.Close()
		}()

	streamLoop:
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					break streamLoop
				}
				renderer.OnEvent(ev)
				absorbEvent(result, ev)
				if ev.FinalText() {
					gotStop = true
				}
			case err := <-decodeErrs:
				if err != nil {
					fmt.Fprintf(spec.Stderr, "[arbiter run] decode: %v\n", err)
				}
			case <-ctx.Done():
				result.Outcome = ctxOutcome(ctx)
				result.Reason = ctx.Err().Error()
			}
		}
	} else {
		// Non-claude CLIs: raw text capture. Same render contract,
		// just wrapped in a fake message_stop so the renderer can
		// finalise.
		var captured strings.Builder
		cmd.Stdout = io.MultiWriter(&captured, logFile, spec.Stdout)
		if err := cmd.Start(); err != nil {
			return finishRefused(r, spec, OutcomeRefused, fmt.Sprintf("spawn %s: %v", abs, err)), nil
		}
		err := cmd.Wait()
		if ctx.Err() != nil {
			result.Outcome = ctxOutcome(ctx)
			result.Reason = ctx.Err().Error()
		}
		if err == nil {
			gotStop = true
		}
		result.FinalText = captured.String()
	}

	endedAt := r.Now()
	result.EndedAt = endedAt
	result.DurationMs = endedAt.Sub(startedAt).Milliseconds()

	if result.Outcome == "" {
		exitCode := 0
		var exitErr *exec.ExitError
		if err := cmd.ProcessState.ExitCode(); err >= 0 {
			exitCode = err
		}
		if errors.As(cmd.Err, &exitErr) || cmd.ProcessState != nil && !cmd.ProcessState.Success() {
			if !gotStop {
				result.Outcome = OutcomeChildFailed
				result.ExitCode = exitCode
				if result.Reason == "" {
					result.Reason = fmt.Sprintf("child exited %d without message_stop", exitCode)
				}
			} else {
				result.Outcome = OutcomeChildFailed
				result.ExitCode = exitCode
				result.Reason = fmt.Sprintf("child exited %d after message_stop", exitCode)
			}
		} else if !gotStop {
			result.Outcome = OutcomeChildFailed
			result.ExitCode = 1
			result.Reason = "stream ended without message_stop"
		} else {
			result.Outcome = OutcomeOK
			result.ExitCode = 0
		}
	} else if result.ExitCode == 0 {
		result.ExitCode = result.Outcome.ExitCode()
	}

	renderer.OnFinish(result)
	finishLogs(spec, result)
	return result, nil
}

// withDefaults backfills any zero-value seam.
func withDefaults(r *Runner) *Runner {
	if r == nil {
		return New()
	}
	if r.LookPath == nil {
		r.LookPath = exec.LookPath
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	return r
}

// resolveSpec fills in cwd, ID, log path, output sinks.
func resolveSpec(r *Runner, spec RunSpec) (RunSpec, error) {
	if spec.CLI == "" {
		spec.CLI = CLIClaude
	}
	if spec.Cwd == "" {
		c, err := os.Getwd()
		if err != nil {
			return spec, fmt.Errorf("resolve cwd: %w", err)
		}
		spec.Cwd = c
	}
	abs, err := filepath.Abs(spec.Cwd)
	if err != nil {
		return spec, fmt.Errorf("resolve cwd: %w", err)
	}
	spec.Cwd = abs
	if spec.ID == "" {
		spec.ID = newRunID(r.Now())
	}
	if spec.LogPath == "" {
		spec.LogPath = LogPathFor(spec.ID)
	} else {
		abs, err := filepath.Abs(spec.LogPath)
		if err != nil {
			return spec, err
		}
		spec.LogPath = abs
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o755); err != nil {
		return spec, fmt.Errorf("ensure log dir: %w", err)
	}
	if spec.OutputMode == "" {
		spec.OutputMode = OutputStream
	}
	if spec.Stdout == nil {
		spec.Stdout = os.Stdout
	}
	if spec.Stderr == nil {
		spec.Stderr = os.Stderr
	}
	if spec.Timeout < 0 {
		return spec, errors.New("timeout must be >= 0")
	}
	return spec, nil
}

// guardUnsafe enforces the documented triple gate on
// --dangerously-skip-permissions.
func guardUnsafe(spec RunSpec) error {
	if os.Getenv("GODX_ARBITER_ALLOW_UNSAFE") != "1" {
		return errors.New("--unsafe-skip-permissions requires GODX_ARBITER_ALLOW_UNSAFE=1 in env")
	}
	// rules.md veto check is the caller's responsibility — they have
	// already loaded the project; the runner stays free of config IO.
	return nil
}

// buildEnv returns the env handed to the child. Default is a curated
// allowlist; --inherit-env passes the caller's full env through.
func buildEnv(spec RunSpec, callerDepth int) []string {
	depth := callerDepth + 1
	allowlist := []string{"PATH", "HOME", "USER", "SHELL", "LANG", "LC_ALL", "TERM", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY", "GOOGLE_API_KEY"}
	out := []string{fmt.Sprintf("ARBITER_RUN_DEPTH=%d", depth)}
	if spec.NoArbiterHooks {
		out = append(out, "GODX_ARBITER_DISABLED=1")
	}
	if spec.InheritEnv {
		// Skip our own ARBITER_RUN_DEPTH and GODX_ARBITER_DISABLED so
		// they're set deterministically above.
		for _, kv := range os.Environ() {
			k := kv
			if i := strings.IndexByte(kv, '='); i >= 0 {
				k = kv[:i]
			}
			if k == "ARBITER_RUN_DEPTH" || k == "GODX_ARBITER_DISABLED" {
				continue
			}
			out = append(out, kv)
		}
		return out
	}
	for _, key := range allowlist {
		if v, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+v)
		}
	}
	return out
}

// absorbEvent updates the running RunResult based on a streamed event.
// Handles both upstream shapes (raw Anthropic streaming + Claude Code
// CLI envelope) so token counts + final text are populated regardless
// of which producer we're talking to.
func absorbEvent(result *RunResult, ev Event) {
	switch ev.Type {
	// --- Anthropic streaming API ---
	case "message_start":
		if ev.Message != nil && ev.Message.Usage != nil {
			result.InputTok = ev.Message.Usage.InputTokens
			result.OutputTok = ev.Message.Usage.OutputTokens
		}
	case "message_delta":
		if ev.Message != nil && ev.Message.Usage != nil {
			result.InputTok = ev.Message.Usage.InputTokens
			result.OutputTok = ev.Message.Usage.OutputTokens
		}
		result.Turns++
	case "content_block_delta":
		if ev.ContentBlockType() == "text_delta" {
			result.FinalText += ev.TextDelta()
		}

	// --- Claude Code CLI envelope ---
	case "assistant":
		if t := ev.AssistantText(); t != "" {
			result.FinalText += t
		}
		if ev.Message != nil && ev.Message.Usage != nil {
			result.InputTok = ev.Message.Usage.InputTokens
			result.OutputTok = ev.Message.Usage.OutputTokens
		}
		result.Turns++
	case "result":
		if ev.ResultUsage != nil {
			// Final aggregate from the CLI; trusted over running totals.
			result.InputTok = ev.ResultUsage.InputTokens
			result.OutputTok = ev.ResultUsage.OutputTokens
		}
		if ev.NumTurns > 0 {
			result.Turns = ev.NumTurns
		}
		if ev.Result != "" && result.FinalText == "" {
			// CLI dumps the full final text in the result event too; use
			// it as a fallback when we missed earlier assistant events.
			result.FinalText = ev.Result
		}
	}
}

// finishLogs writes the closing eventlog row + index entry.
func finishLogs(spec RunSpec, result *RunResult) {
	_ = AppendIndex(IndexEntry{
		ID:       spec.ID,
		CLI:      spec.CLI,
		Model:    spec.Model,
		Cwd:      spec.Cwd,
		Started:  result.StartedAt,
		Ended:    result.EndedAt,
		ExitCode: result.ExitCode,
		Outcome:  result.Outcome,
		LogPath:  spec.LogPath,
		Reason:   result.Reason,
	})
	_ = eventlog.Append(eventlog.Event{
		TS:         result.EndedAt,
		EventID:    spec.ID + "-end",
		SessionID:  spec.ID,
		Project:    spec.Cwd,
		Tool:       string(spec.CLI),
		InputSum:   truncate(spec.Task, 240),
		Path:       "run",
		Decision:   string(result.Outcome),
		Reason:     result.Reason,
		DurationMs: result.DurationMs,
		Run: &eventlog.RunInfo{
			CLI:       string(spec.CLI),
			Model:     spec.Model,
			ExitCode:  result.ExitCode,
			Outcome:   string(result.Outcome),
			LogPath:   spec.LogPath,
			InputTok:  result.InputTok,
			OutputTok: result.OutputTok,
			Turns:     result.Turns,
		},
	})
}

// finishRefused short-circuits when we refuse to spawn at all (bad
// arg, missing binary, recursion fuse). One eventlog row, exit code
// 2.
func finishRefused(r *Runner, spec RunSpec, outcome Outcome, reason string) *RunResult {
	now := r.Now()
	res := &RunResult{
		ID:        spec.ID,
		CLI:       spec.CLI,
		Cwd:       spec.Cwd,
		StartedAt: now,
		EndedAt:   now,
		Outcome:   outcome,
		ExitCode:  outcome.ExitCode(),
		Reason:    reason,
	}
	_ = eventlog.Append(eventlog.Event{
		TS:        now,
		EventID:   spec.ID,
		SessionID: spec.ID,
		Project:   spec.Cwd,
		Tool:      string(spec.CLI),
		InputSum:  truncate(spec.Task, 240),
		Path:      "run",
		Decision:  string(outcome),
		Reason:    reason,
	})
	return res
}

// ctxOutcome maps a cancelled context to an outcome.
func ctxOutcome(ctx context.Context) Outcome {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return OutcomeTimeout
	}
	return OutcomeInterrupted
}

// newRunID creates a stable, sortable id with random suffix. The
// suffix prevents collisions when two runs start in the same
// microsecond from different shells.
func newRunID(now time.Time) string {
	var buf [3]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("run-%s-%s", now.UTC().Format("20060102-150405"), hex.EncodeToString(buf[:]))
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// killGroup is provided per-platform in sysproc_{unix,windows}.go.
