package hookio

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestReadInput_PreToolUse(t *testing.T) {
	raw := `{
		"session_id": "abc-123",
		"cwd": "/home/u/proj",
		"hook_event_name": "PreToolUse",
		"tool_name": "Bash",
		"tool_input": {"command": "ls -la", "description": "list files"}
	}`
	in, err := ReadInput(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadInput: %v", err)
	}
	if in.SessionID != "abc-123" {
		t.Errorf("SessionID = %q", in.SessionID)
	}
	if in.Cwd != "/home/u/proj" {
		t.Errorf("Cwd = %q", in.Cwd)
	}
	if in.ToolName != "Bash" {
		t.Errorf("ToolName = %q", in.ToolName)
	}
	if len(in.ToolInput) == 0 {
		t.Error("ToolInput is empty")
	}
	var ti map[string]any
	if err := json.Unmarshal(in.ToolInput, &ti); err != nil {
		t.Fatalf("ToolInput unmarshal: %v", err)
	}
	if ti["command"] != "ls -la" {
		t.Errorf("ToolInput.command = %v", ti["command"])
	}
}

func TestReadInput_Notification(t *testing.T) {
	raw := `{"session_id":"x","hook_event_name":"Notification","message":"Need attention"}`
	in, err := ReadInput(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadInput: %v", err)
	}
	if in.Message != "Need attention" {
		t.Errorf("Message = %q", in.Message)
	}
}

func TestReadInput_Empty(t *testing.T) {
	if _, err := ReadInput(strings.NewReader("")); err == nil {
		t.Error("expected error on empty input")
	}
}

func TestReadInput_Malformed(t *testing.T) {
	if _, err := ReadInput(strings.NewReader("not json")); err == nil {
		t.Error("expected error on malformed input")
	}
}

func TestWriteAllow(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAllow(&buf, ""); err != nil {
		t.Fatal(err)
	}
	out := decodeOutput(t, buf.Bytes())
	if out.HookSpecificOutput == nil {
		t.Fatal("HookSpecificOutput is nil")
	}
	hso := out.HookSpecificOutput
	if hso.PermissionDecision != DecisionAllow {
		t.Errorf("PermissionDecision = %q", hso.PermissionDecision)
	}
	if hso.HookEventName != "PreToolUse" {
		t.Errorf("HookEventName = %q", hso.HookEventName)
	}
	if hso.PermissionDecisionReason != "" {
		t.Errorf("Reason should be empty, got %q", hso.PermissionDecisionReason)
	}
}

func TestWriteAllow_WithReason(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAllow(&buf, "policy.yaml allow[2]"); err != nil {
		t.Fatal(err)
	}
	out := decodeOutput(t, buf.Bytes())
	if out.HookSpecificOutput.PermissionDecisionReason != "policy.yaml allow[2]" {
		t.Errorf("Reason = %q", out.HookSpecificOutput.PermissionDecisionReason)
	}
}

func TestWriteAllowWithMeta(t *testing.T) {
	var buf bytes.Buffer
	meta := map[string]any{"path": "fast-path", "duration_ms": 4}
	if err := WriteAllowWithMeta(&buf, "ok", meta); err != nil {
		t.Fatal(err)
	}
	out := decodeOutput(t, buf.Bytes())
	if out.Metadata["path"] != "fast-path" {
		t.Errorf("metadata.path = %v", out.Metadata["path"])
	}
}

func TestWriteDeny(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDeny(&buf, "rm -rf forbidden"); err != nil {
		t.Fatal(err)
	}
	out := decodeOutput(t, buf.Bytes())
	hso := out.HookSpecificOutput
	if hso.PermissionDecision != DecisionDeny {
		t.Errorf("PermissionDecision = %q", hso.PermissionDecision)
	}
	if hso.PermissionDecisionReason != "rm -rf forbidden" {
		t.Errorf("Reason = %q", hso.PermissionDecisionReason)
	}
}

func TestWriteAsk(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAsk(&buf, "no rule, surface to user"); err != nil {
		t.Fatal(err)
	}
	out := decodeOutput(t, buf.Bytes())
	if out.HookSpecificOutput.PermissionDecision != DecisionAsk {
		t.Errorf("PermissionDecision = %q", out.HookSpecificOutput.PermissionDecision)
	}
}

func TestWriteEdit(t *testing.T) {
	var buf bytes.Buffer
	updated := map[string]any{"command": "ls -la"}
	if err := WriteEdit(&buf, updated, "stripped sketchy flag"); err != nil {
		t.Fatal(err)
	}
	out := decodeOutput(t, buf.Bytes())
	hso := out.HookSpecificOutput
	if hso.PermissionDecision != DecisionAllow {
		t.Errorf("edit must use allow; got %q", hso.PermissionDecision)
	}
	if hso.UpdatedInput["command"] != "ls -la" {
		t.Errorf("UpdatedInput.command = %v", hso.UpdatedInput["command"])
	}
}

func TestWriteDefer(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDefer(&buf); err != nil {
		t.Fatal(err)
	}
	out := decodeOutput(t, buf.Bytes())
	if out.HookSpecificOutput.PermissionDecision != DecisionDefer {
		t.Errorf("PermissionDecision = %q", out.HookSpecificOutput.PermissionDecision)
	}
}

func TestOutput_JSONShape_Snapshot(t *testing.T) {
	// Lock down the on-the-wire shape — agents downstream care.
	var buf bytes.Buffer
	if err := WriteAllowWithMeta(&buf, "rule", map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(buf.String())
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"rule"},"metadata":{"k":"v"}}`
	if got != want {
		t.Errorf("shape drift:\n got=%s\nwant=%s", got, want)
	}
}

func decodeOutput(t *testing.T, raw []byte) Output {
	t.Helper()
	var out Output
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, raw)
	}
	return out
}
