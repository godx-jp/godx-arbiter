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
		t.Errorf("SessionID = %q, want abc-123", in.SessionID)
	}
	if in.Cwd != "/home/u/proj" {
		t.Errorf("Cwd = %q, want /home/u/proj", in.Cwd)
	}
	if in.ToolName != "Bash" {
		t.Errorf("ToolName = %q, want Bash", in.ToolName)
	}
	if len(in.ToolInput) == 0 {
		t.Error("ToolInput is empty")
	}
	// Round-trip the raw tool_input to confirm it parses as a sub-object.
	var ti map[string]any
	if err := json.Unmarshal(in.ToolInput, &ti); err != nil {
		t.Fatalf("ToolInput unmarshal: %v", err)
	}
	if ti["command"] != "ls -la" {
		t.Errorf("ToolInput.command = %v, want 'ls -la'", ti["command"])
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

func TestWriteApprove_Bare(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteApprove(&buf, ""); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(buf.String())
	if got != `{"decision":"approve"}` {
		t.Errorf("got %q", got)
	}
}

func TestWriteApprove_WithReason(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteApprove(&buf, "step-1-stub: Read"); err != nil {
		t.Fatal(err)
	}
	var out Output
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if out.Decision != "approve" {
		t.Errorf("decision = %q", out.Decision)
	}
	if out.Reason != "step-1-stub: Read" {
		t.Errorf("reason = %q", out.Reason)
	}
}

func TestWriteBlock(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteBlock(&buf, "rm -rf forbidden"); err != nil {
		t.Fatal(err)
	}
	var out Output
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Decision != "block" {
		t.Errorf("decision = %q, want block", out.Decision)
	}
	if out.Reason != "rm -rf forbidden" {
		t.Errorf("reason = %q", out.Reason)
	}
}

func TestWriteWithMetadata(t *testing.T) {
	var buf bytes.Buffer
	meta := map[string]any{"path": "fast-path", "duration_ms": 4}
	if err := WriteWithMetadata(&buf, "approve", "policy.yaml allow[0]", meta); err != nil {
		t.Fatal(err)
	}
	var out Output
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Metadata["path"] != "fast-path" {
		t.Errorf("metadata.path = %v", out.Metadata["path"])
	}
}
