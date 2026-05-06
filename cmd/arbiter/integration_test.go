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
