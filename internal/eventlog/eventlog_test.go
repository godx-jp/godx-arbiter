package eventlog

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndLookup_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for i := 0; i < 3; i++ {
		if err := AppendTo(path, Event{
			SessionID: "abc",
			Tool:      "Bash",
			InputSum:  "rm -rf node_modules",
			Path:      "fast-path",
			Decision:  "deny",
			Reason:    "rules.md §destructive",
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// Add one mismatch to verify filtering works.
	if err := AppendTo(path, Event{
		SessionID: "xyz",
		Tool:      "Edit",
		InputSum:  "edit foo.go",
		Path:      "fast-path",
		Decision:  "allow",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := Lookup(LookupOpts{Tool: "Bash", Pattern: "node_modules", Limit: 5, Path: path})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	for _, ev := range got {
		if ev.Tool != "Bash" {
			t.Errorf("tool = %q", ev.Tool)
		}
		if ev.EventID == "" {
			t.Error("event id was not assigned")
		}
		if ev.TS.IsZero() {
			t.Error("ts was not assigned")
		}
	}
}

func TestLookup_Limit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for i := 0; i < 10; i++ {
		_ = AppendTo(path, Event{Tool: "Bash", InputSum: "x", Decision: "allow"})
	}
	got, err := Lookup(LookupOpts{Tool: "Bash", Limit: 3, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %d, want 3", len(got))
	}
}

func TestExplain_Last(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	_ = AppendTo(path, Event{TS: time.Now().Add(-1 * time.Hour), SessionID: "old", Tool: "Bash", Decision: "allow"})
	_ = AppendTo(path, Event{TS: time.Now(), SessionID: "new", Tool: "Edit", Decision: "deny", Reason: "secret file"})

	out, err := Explain(ExplainOpts{Last: true, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if want := "session:   new"; !contains(out, want) {
		t.Errorf("output missing %q: %s", want, out)
	}
	if want := "decision:  deny"; !contains(out, want) {
		t.Errorf("output missing %q: %s", want, out)
	}
}

func TestExplain_NoEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	out, err := Explain(ExplainOpts{Last: true, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "no decisions logged yet") {
		t.Errorf("expected friendly empty message, got: %s", out)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
