package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDefaultRegistry_HasExpectedTools(t *testing.T) {
	r := DefaultRegistry()
	want := []string{
		"analyze_risk", "check_rule", "get_project_meta",
		"list_recent_actions", "lookup_history", "read_file",
	}
	for _, name := range want {
		if _, ok := r.Get(name); !ok {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestAnalyzeRisk_RmRfNodeModules(t *testing.T) {
	in, _ := json.Marshal(map[string]any{
		"tool":  "Bash",
		"input": map[string]any{"command": "rm -rf node_modules"},
	})
	out, err := DefaultRegistry().Execute(context.Background(), "analyze_risk", in)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["category"] != "destructive-reversible" {
		t.Errorf("category = %v", got["category"])
	}
}

func TestAnalyzeRisk_CatastrophicRm(t *testing.T) {
	in, _ := json.Marshal(map[string]any{
		"tool":  "Bash",
		"input": map[string]any{"command": "rm -rf /etc/foo"},
	})
	out, _ := DefaultRegistry().Execute(context.Background(), "analyze_risk", in)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["category"] != "catastrophic" {
		t.Errorf("category = %v", got["category"])
	}
	if score := got["score"].(float64); score < 0.9 {
		t.Errorf("score too low: %v", score)
	}
}

func TestAnalyzeRisk_Read(t *testing.T) {
	in, _ := json.Marshal(map[string]any{"tool": "Read", "input": map[string]any{"file_path": "/x"}})
	out, _ := DefaultRegistry().Execute(context.Background(), "analyze_risk", in)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["category"] != "read-only" {
		t.Errorf("category = %v", got["category"])
	}
}

func TestReadFile_RefusesEscape(t *testing.T) {
	root := t.TempDir()
	in, _ := json.Marshal(map[string]any{"project_root": root, "path": "../../../etc/passwd"})
	if _, err := DefaultRegistry().Execute(context.Background(), "read_file", in); err == nil {
		t.Error("expected error for path escape")
	}
}

func TestUnknownTool(t *testing.T) {
	if _, err := DefaultRegistry().Execute(context.Background(), "no_such_tool", nil); err == nil {
		t.Error("expected error")
	}
}
