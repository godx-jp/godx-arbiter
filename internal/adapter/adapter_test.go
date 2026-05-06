package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeCode_Roundtrip(t *testing.T) {
	a := NewClaudeCode()
	raw := []byte(`{"session_id":"abc","cwd":"/p","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	ev, err := a.ParseEvent(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Phase != PhasePreTool {
		t.Errorf("phase = %q", ev.Phase)
	}
	if ev.Tool.Name != "Bash" {
		t.Errorf("tool name = %q", ev.Tool.Name)
	}
	out, err := a.EncodeDecision(context.Background(), ev, Decision{Outcome: "allow", Reason: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	hso := decoded["hookSpecificOutput"].(map[string]any)
	if hso["hookEventName"] != "PreToolUse" {
		t.Errorf("hookEventName = %v", hso["hookEventName"])
	}
	if hso["permissionDecision"] != "allow" {
		t.Errorf("permissionDecision = %v", hso["permissionDecision"])
	}
}

func TestRegistry_HasAllAdapters(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"claude-code", "codex", "gemini", "antigravity"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("missing adapter %q", name)
		}
	}
}

func TestGemini_DecisionMapping(t *testing.T) {
	out, _ := NewGemini().EncodeDecision(context.Background(), CanonicalEvent{}, Decision{Outcome: "deny", Reason: "no"})
	if !strings.Contains(string(out), `"decision":"block"`) {
		t.Errorf("expected block, got %s", out)
	}
}

func TestCodex_GenericParse(t *testing.T) {
	raw := []byte(`{"session_id":"x","tool_name":"Edit","tool_input":{"file_path":"a.go"}}`)
	ev, err := NewCodex().ParseEvent(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if ev.CLI != "codex" {
		t.Errorf("cli = %q", ev.CLI)
	}
	if ev.Tool.Name != "Edit" {
		t.Errorf("tool = %q", ev.Tool.Name)
	}
}
