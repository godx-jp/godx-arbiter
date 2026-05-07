package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeClaudePath returns the absolute path to the testdata fake claude
// binary. Tests prepend this to PATH so exec.LookPath resolves to it.
func fakeClaudePath(t *testing.T) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata", "bin", "fake-claude.sh")
}

// withFakeClaude makes the fake claude script resolvable as `claude`
// for the duration of the test.
func withFakeClaude(t *testing.T) string {
	t.Helper()
	src := fakeClaudePath(t)
	binDir := t.TempDir()
	dst := filepath.Join(binDir, "claude")
	// Symlink keeps file mode + lets PATH find it as `claude`.
	if err := os.Symlink(src, dst); err != nil {
		t.Fatalf("symlink fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dst
}

func setupRunnerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GODX_ARBITER_HOME", t.TempDir())
	t.Setenv("GODX_ARBITER_RUNS_DIR", filepath.Join(t.TempDir(), "runs"))
	t.Setenv("GODX_ARBITER_LOG_PATH", filepath.Join(t.TempDir(), "events.jsonl"))
	// Reset any depth tracker that previous tests may have set in this
	// process (env is process-global; subtests don't reset it).
	t.Setenv("ARBITER_RUN_DEPTH", "0")
}

func TestRun_HappyPath(t *testing.T) {
	setupRunnerEnv(t)
	withFakeClaude(t)
	t.Setenv("FAKE_CLAUDE_MODE", "ok")

	var stdout, stderr bytes.Buffer
	r := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := r.Run(ctx, RunSpec{
		CLI:        CLIClaude,
		Task:       "say hi",
		Cwd:        t.TempDir(),
		OutputMode: OutputStream,
		Stdout:     &stdout,
		Stderr:     &stderr,
		InheritEnv: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Outcome != OutcomeOK {
		t.Errorf("outcome = %q, want completed (stderr=%s)", result.Outcome, stderr.String())
	}
	if result.ExitCode != 0 {
		t.Errorf("exit = %d", result.ExitCode)
	}
	if !strings.Contains(stdout.String(), "hello world") {
		t.Errorf("stdout missing rendered text: %s", stdout.String())
	}
	if result.OutputTok != 2 {
		t.Errorf("output tokens = %d (want 2)", result.OutputTok)
	}
}

func TestRun_MidStreamFailure(t *testing.T) {
	setupRunnerEnv(t)
	withFakeClaude(t)
	t.Setenv("FAKE_CLAUDE_MODE", "midfail")

	var stdout, stderr bytes.Buffer
	r := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, _ := r.Run(ctx, RunSpec{
		CLI: CLIClaude, Task: "x", Cwd: t.TempDir(),
		OutputMode: OutputStream, Stdout: &stdout, Stderr: &stderr,
		InheritEnv: true,
	})
	if result.Outcome != OutcomeChildFailed {
		t.Errorf("outcome = %q, want failed (stderr=%s)", result.Outcome, stderr.String())
	}
	if result.ExitCode != 1 {
		t.Errorf("exit = %d", result.ExitCode)
	}
}

func TestRun_TimeoutKillsChild(t *testing.T) {
	setupRunnerEnv(t)
	withFakeClaude(t)
	t.Setenv("FAKE_CLAUDE_MODE", "sleep")

	var stdout, stderr bytes.Buffer
	r := New()
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	start := time.Now()
	result, _ := r.Run(ctx, RunSpec{
		CLI: CLIClaude, Task: "x", Cwd: t.TempDir(),
		OutputMode: OutputStream, Stdout: &stdout, Stderr: &stderr,
		InheritEnv: true,
	})
	elapsed := time.Since(start)
	if result.Outcome != OutcomeTimeout {
		t.Errorf("outcome = %q, want timeout (stderr=%s)", result.Outcome, stderr.String())
	}
	if result.ExitCode != 124 {
		t.Errorf("exit = %d, want 124", result.ExitCode)
	}
	if elapsed > 8*time.Second {
		t.Errorf("runner took %v to honour timeout — process group teardown stalled", elapsed)
	}
}

func TestRun_PromptReachesChild(t *testing.T) {
	setupRunnerEnv(t)
	withFakeClaude(t)
	t.Setenv("FAKE_CLAUDE_MODE", "echo")

	var stdout bytes.Buffer
	r := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prompt := "task=verify-stdin-routing"
	result, err := r.Run(ctx, RunSpec{
		CLI: CLIClaude, Task: prompt, Cwd: t.TempDir(),
		OutputMode: OutputStream, Stdout: &stdout, Stderr: &bytes.Buffer{},
		InheritEnv: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "verify-stdin-routing") {
		t.Errorf("prompt didn't reach the child: stdout=%q final=%q", stdout.String(), result.FinalText)
	}
}

func TestRun_RefusesPATHHijack(t *testing.T) {
	setupRunnerEnv(t)
	binDir := t.TempDir()
	// Create a binary named "claudepretender" that won't satisfy the
	// basename match. We invoke via spec.CLI = "claude" so LookPath
	// should fail (no `claude` on PATH).
	t.Setenv("PATH", binDir)

	r := New()
	ctx := context.Background()
	result, _ := r.Run(ctx, RunSpec{
		CLI: CLIClaude, Task: "x", Cwd: t.TempDir(),
		OutputMode: OutputFinal, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	if result.Outcome != OutcomeRefused {
		t.Errorf("outcome = %q, want refused", result.Outcome)
	}
	if result.ExitCode != 2 {
		t.Errorf("exit = %d, want 2", result.ExitCode)
	}
}

func TestRun_RefusesUnsafeWithoutEnv(t *testing.T) {
	setupRunnerEnv(t)
	withFakeClaude(t)
	// Make sure the env gate is OFF.
	t.Setenv("GODX_ARBITER_ALLOW_UNSAFE", "")

	r := New()
	ctx := context.Background()
	result, _ := r.Run(ctx, RunSpec{
		CLI: CLIClaude, Task: "x", Cwd: t.TempDir(),
		OutputMode:            OutputFinal,
		Stdout:                &bytes.Buffer{},
		Stderr:                &bytes.Buffer{},
		UnsafeSkipPermissions: true,
	})
	if result.Outcome != OutcomeRefused {
		t.Errorf("outcome = %q, want refused (env gate)", result.Outcome)
	}
	if !strings.Contains(result.Reason, "GODX_ARBITER_ALLOW_UNSAFE") {
		t.Errorf("reason should mention the env gate: %q", result.Reason)
	}
}

func TestRun_RefusesRecursionFuse(t *testing.T) {
	setupRunnerEnv(t)
	withFakeClaude(t)
	t.Setenv("ARBITER_RUN_DEPTH", "5")

	r := New()
	ctx := context.Background()
	result, _ := r.Run(ctx, RunSpec{
		CLI: CLIClaude, Task: "x", Cwd: t.TempDir(),
		OutputMode: OutputFinal, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	if result.Outcome != OutcomeRefused {
		t.Errorf("outcome = %q, want refused", result.Outcome)
	}
	if !strings.Contains(strings.ToLower(result.Reason), "depth") {
		t.Errorf("reason should mention depth: %q", result.Reason)
	}
}

// TestRun_NewRunIDIsUnique guards against two simultaneous runs in the
// same second producing colliding ids.
func TestRun_NewRunIDIsUnique(t *testing.T) {
	now := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		id := newRunID(now)
		if seen[id] {
			t.Errorf("collision on iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

// Sanity: BuildInvocation injects --output-format stream-json for
// claude even in OutputFinal mode (we always want the structured
// events upstream — render mode just controls what the user sees).
func TestBuildInvocation_ClaudeAlwaysStreamJSON(t *testing.T) {
	for _, mode := range []OutputMode{OutputStream, OutputFinal, OutputJSON} {
		inv, err := BuildInvocation(RunSpec{CLI: CLIClaude, Task: "x", OutputMode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if !contains(inv.Args, "stream-json") {
			t.Errorf("mode=%s: missing stream-json in args: %v", mode, inv.Args)
		}
	}
}

// Sanity: BuildInvocation does NOT inject stream-json for non-claude
// CLIs (codex / gemini / antigravity emit raw text).
func TestBuildInvocation_NonClaudeRawText(t *testing.T) {
	for _, cli := range []CLI{CLICodex, CLIGemini, CLIAntigravity} {
		inv, err := BuildInvocation(RunSpec{CLI: cli, Task: "x", OutputMode: OutputStream})
		if err != nil {
			t.Fatalf("cli=%s: %v", cli, err)
		}
		if contains(inv.Args, "stream-json") {
			t.Errorf("cli=%s leaked stream-json: %v", cli, inv.Args)
		}
	}
}

// Buffered-renderer regression: passing nil ctx error sources
// shouldn't panic.
func TestRun_GuardsNilStdout(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic: %v", r)
		}
	}()
	setupRunnerEnv(t)
	withFakeClaude(t)
	t.Setenv("FAKE_CLAUDE_MODE", "ok")
	r := New()
	_, err := r.Run(context.Background(), RunSpec{
		CLI: CLIClaude, Task: "x", Cwd: t.TempDir(),
		// Stdout/Stderr left nil intentionally.
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
