package main_test

// End-to-end integration tests: build the actual binary, fire it as a
// subprocess, pipe synthetic hook payloads, assert on the JSON
// response. Catches main.go regressions that unit tests in subpackages
// miss (subcommand routing, default writers, fail-open paths).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildBinary compiles the arbiter binary into a temp dir once per
// test process. Returns its path.
var binPath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "arbiter-integration-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	binPath = filepath.Join(tmp, "arbiter")

	cmd := exec.Command("go", "build", "-o", binPath, "github.com/godx-team/godx-arbiter/cmd/arbiter")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func runHook(t *testing.T, args []string, stdin string, env ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), env...)
	err := cmd.Run()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("run %v: %v\nstderr=%s", args, err, stderr.String())
		}
	}
	return stdout.String(), stderr.String(), exit
}

func TestIntegration_HookPretool_ReadAllowed(t *testing.T) {
	stdin := `{"session_id":"itest","cwd":"/tmp","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/etc/hostname"}}`
	stdout, _, exit := runHook(t, []string{"hook", "pretool"}, stdin, "GODX_ARBITER_HOME="+t.TempDir())
	if exit != 0 {
		t.Fatalf("exit = %d, stdout=%s", exit, stdout)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse stdout: %v\n%s", err, stdout)
	}
	hso, ok := out["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("missing hookSpecificOutput in %v", out)
	}
	if hso["permissionDecision"] != "allow" {
		t.Errorf("decision = %v", hso["permissionDecision"])
	}
}

func TestIntegration_HookPretool_DenyFromPolicy(t *testing.T) {
	dir := t.TempDir()
	arb := filepath.Join(dir, ".arbiter")
	if err := os.MkdirAll(arb, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := `version: 1
default: agent
deny:
  - tool: Bash
    pattern: 'rm -rf /etc'
    reason: system path
`
	if err := os.WriteFile(filepath.Join(arb, "policy.yaml"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	stdin := `{"session_id":"itest","cwd":"` + dir + `","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /etc/foo"}}`
	stdout, _, exit := runHook(t, []string{"hook", "pretool"}, stdin, "GODX_ARBITER_HOME="+t.TempDir())
	if exit != 0 {
		t.Fatalf("exit = %d, stdout=%s", exit, stdout)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(stdout), &out)
	hso := out["hookSpecificOutput"].(map[string]any)
	if hso["permissionDecision"] != "deny" {
		t.Errorf("decision = %v", hso["permissionDecision"])
	}
	if !strings.Contains(hso["permissionDecisionReason"].(string), "system path") {
		t.Errorf("reason = %v", hso["permissionDecisionReason"])
	}
}

func TestIntegration_HookPretool_KillSwitch(t *testing.T) {
	stdin := `{"session_id":"itest","cwd":"/tmp","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /etc/foo"}}`
	stdout, _, exit := runHook(t, []string{"hook", "pretool"}, stdin,
		"GODX_ARBITER_HOME="+t.TempDir(), "GODX_ARBITER_DISABLED=1")
	if exit != 0 {
		t.Fatalf("exit = %d, stdout=%s", exit, stdout)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(stdout), &out)
	hso := out["hookSpecificOutput"].(map[string]any)
	if hso["permissionDecision"] != "allow" {
		t.Errorf("kill switch should allow, got %v", hso["permissionDecision"])
	}
	meta := out["metadata"].(map[string]any)
	if meta["path"] != "kill-switch" {
		t.Errorf("path = %v", meta["path"])
	}
}

func TestIntegration_HookPretool_FailOpenOnEmptyStdin(t *testing.T) {
	stdout, _, exit := runHook(t, []string{"hook", "pretool"}, "", "GODX_ARBITER_HOME="+t.TempDir())
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("expected JSON on empty stdin too, got: %s", stdout)
	}
	hso := out["hookSpecificOutput"].(map[string]any)
	if hso["permissionDecision"] != "allow" {
		t.Errorf("ADR-005 fail-open expected allow, got %v", hso["permissionDecision"])
	}
}

func TestIntegration_VersionExitCode(t *testing.T) {
	stdout, _, exit := runHook(t, []string{"version"}, "")
	if exit != 0 {
		t.Errorf("exit = %d", exit)
	}
	if !strings.Contains(stdout, "godx-arbiter") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestIntegration_DoctorJSON(t *testing.T) {
	stdout, _, exit := runHook(t, []string{"doctor", "--json"}, "", "GODX_ARBITER_HOME="+t.TempDir())
	if exit != 0 {
		t.Fatalf("exit = %d, stdout=%s", exit, stdout)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("doctor --json must emit valid JSON; got: %s", stdout)
	}
	if rep["version"] == nil {
		t.Errorf("missing version: %v", rep)
	}
	if _, ok := rep["env"].(map[string]any); !ok {
		t.Errorf("missing env section: %v", rep)
	}
}

// withFakeClaude prepends the fake-claude script (as `claude`) to PATH
// for `arbiter run` integration tests. Returns the env entries the
// caller should pass to runHook so the override sticks.
func withFakeClaudeForRun(t *testing.T, mode string) []string {
	t.Helper()
	src := filepath.Join("..", "..", "internal", "runner", "testdata", "bin", "fake-claude.sh")
	abs, err := filepath.Abs(src)
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.Symlink(abs, filepath.Join(binDir, "claude")); err != nil {
		t.Fatalf("symlink fake claude: %v", err)
	}
	return []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_CLAUDE_MODE=" + mode,
	}
}

func TestIntegration_Run_HappyPath(t *testing.T) {
	home := t.TempDir()
	env := append(
		withFakeClaudeForRun(t, "ok"),
		"GODX_ARBITER_HOME="+home,
		"GODX_ARBITER_RUNS_DIR="+filepath.Join(home, "runs"),
	)
	stdout, stderr, exit := runHook(t,
		[]string{"run", "--quiet", "--inherit-env", "--cwd", t.TempDir(), "--timeout", "10s", "--", "say hi"},
		"", env...)
	if exit != 0 {
		t.Fatalf("exit = %d\nstdout=%s\nstderr=%s", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "completed") {
		t.Errorf("expected 'completed' in stderr summary: %s", stderr)
	}
}

func TestIntegration_Run_MidStreamFailureMapsExit1(t *testing.T) {
	home := t.TempDir()
	env := append(
		withFakeClaudeForRun(t, "midfail"),
		"GODX_ARBITER_HOME="+home,
		"GODX_ARBITER_RUNS_DIR="+filepath.Join(home, "runs"),
	)
	_, _, exit := runHook(t,
		[]string{"run", "--quiet", "--inherit-env", "--cwd", t.TempDir(), "--timeout", "10s", "--", "x"},
		"", env...)
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
}

func TestIntegration_Run_TimeoutMapsExit124(t *testing.T) {
	home := t.TempDir()
	env := append(
		withFakeClaudeForRun(t, "sleep"),
		"GODX_ARBITER_HOME="+home,
		"GODX_ARBITER_RUNS_DIR="+filepath.Join(home, "runs"),
	)
	start := time.Now()
	_, _, exit := runHook(t,
		[]string{"run", "--quiet", "--inherit-env", "--cwd", t.TempDir(), "--timeout", "1s", "--", "x"},
		"", env...)
	elapsed := time.Since(start)
	if exit != 124 {
		t.Errorf("exit = %d, want 124", exit)
	}
	if elapsed > 9*time.Second {
		t.Errorf("timeout took %v — process group teardown stalled", elapsed)
	}
}

func TestIntegration_Run_UnsafeRefusedWithoutEnv(t *testing.T) {
	home := t.TempDir()
	env := append(
		withFakeClaudeForRun(t, "ok"),
		"GODX_ARBITER_HOME="+home,
		"GODX_ARBITER_RUNS_DIR="+filepath.Join(home, "runs"),
		"GODX_ARBITER_ALLOW_UNSAFE=", // explicitly empty
	)
	stdout, stderr, exit := runHook(t,
		[]string{"run", "--quiet", "--inherit-env", "--cwd", t.TempDir(), "--unsafe-skip-permissions", "--", "x"},
		"", env...)
	if exit != 2 {
		t.Errorf("exit = %d, want 2 (unsafe refused)\nstdout=%s\nstderr=%s", exit, stdout, stderr)
	}
}

func TestIntegration_Run_RunListEmpty(t *testing.T) {
	home := t.TempDir()
	env := []string{
		"GODX_ARBITER_HOME=" + home,
		"GODX_ARBITER_RUNS_DIR=" + filepath.Join(home, "runs"),
	}
	stdout, _, exit := runHook(t, []string{"run", "--list"}, "", env...)
	if exit != 0 {
		t.Errorf("exit = %d", exit)
	}
	if !strings.Contains(stdout, "no runs recorded yet") {
		t.Errorf("unexpected stdout: %s", stdout)
	}
}

func TestIntegration_Run_HelpInUsage(t *testing.T) {
	stdout, _, _ := runHook(t, []string{"help"}, "")
	if !strings.Contains(stdout, "run [flags]") {
		t.Errorf("printUsage missing arbiter run section: %s", stdout)
	}
	if !strings.Contains(stdout, "--resume ID") && !strings.Contains(stdout, "--resume") {
		t.Errorf("printUsage missing --resume flag mention: %s", stdout)
	}
}
