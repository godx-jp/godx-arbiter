package runner

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndFindLatest(t *testing.T) {
	t.Setenv("GODX_ARBITER_RUNS_DIR", t.TempDir())
	now := time.Now()
	if err := AppendIndex(IndexEntry{ID: "r1", CLI: CLIClaude, Cwd: "/tmp/p1", Started: now}); err != nil {
		t.Fatal(err)
	}
	if err := AppendIndex(IndexEntry{ID: "r1", CLI: CLIClaude, Cwd: "/tmp/p1", Started: now, Ended: now.Add(time.Second), Outcome: OutcomeOK, ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	got, err := FindLatest("r1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("nil result")
	}
	if got.Outcome != OutcomeOK {
		t.Errorf("outcome = %q (want completed)", got.Outcome)
	}
	if got.Ended.IsZero() {
		t.Errorf("end-row should be returned, got start-row")
	}
}

func TestFindLatest_NoSuchID(t *testing.T) {
	t.Setenv("GODX_ARBITER_RUNS_DIR", t.TempDir())
	got, err := FindLatest("nope")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestVerifyResumeCwd_Mismatch(t *testing.T) {
	t.Setenv("GODX_ARBITER_RUNS_DIR", t.TempDir())
	_ = AppendIndex(IndexEntry{ID: "rA", CLI: CLIClaude, Cwd: "/tmp/orig", Started: time.Now(), Ended: time.Now().Add(time.Second), Outcome: OutcomeOK})
	if err := VerifyResumeCwd("rA", "/tmp/different", false); err == nil {
		t.Errorf("expected refusal on cwd mismatch")
	}
	if err := VerifyResumeCwd("rA", "/tmp/different", true); err != nil {
		t.Errorf("--force-resume should override: %v", err)
	}
	if err := VerifyResumeCwd("rA", "/tmp/orig", false); err != nil {
		t.Errorf("matching cwd should not error: %v", err)
	}
}

func TestVerifyResumeCwd_UnknownIDPassesThrough(t *testing.T) {
	t.Setenv("GODX_ARBITER_RUNS_DIR", t.TempDir())
	if err := VerifyResumeCwd("never-seen", "/tmp/anywhere", false); err != nil {
		t.Errorf("unknown id should not error (claude itself rejects bogus id): %v", err)
	}
}

func TestListRecent_LatestFirst(t *testing.T) {
	t.Setenv("GODX_ARBITER_RUNS_DIR", t.TempDir())
	t0 := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		_ = AppendIndex(IndexEntry{
			ID: id, CLI: CLIClaude, Cwd: "/p",
			Started: t0.Add(time.Duration(i) * time.Minute),
			Ended:   t0.Add(time.Duration(i)*time.Minute + 30*time.Second),
			Outcome: OutcomeOK,
		})
	}
	got, err := ListRecent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries", len(got))
	}
	if got[0].ID != "c" || got[2].ID != "a" {
		t.Errorf("order wrong: %v", []string{got[0].ID, got[1].ID, got[2].ID})
	}
}

func TestLogPathFor(t *testing.T) {
	t.Setenv("GODX_ARBITER_RUNS_DIR", "/x")
	got := LogPathFor("run-123")
	want := filepath.Join("/x", "run-123.jsonl")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
