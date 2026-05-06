package usage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReport_Basic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	now := time.Now()
	for _, r := range []Record{
		{TS: now, SessionID: "abc", CLI: "claude-code", Model: "claude-sonnet-4-6", InputTokens: 1000, OutputTokens: 200, CostUSD: 0.01},
		{TS: now, SessionID: "abc", CLI: "claude-code", Model: "claude-sonnet-4-6", InputTokens: 500, OutputTokens: 100, CostUSD: 0.005},
		{TS: now, SessionID: "def", CLI: "codex", Model: "gpt-5", InputTokens: 800, OutputTokens: 150, CostUSD: 0.012},
	} {
		if err := AppendTo(path, r); err != nil {
			t.Fatal(err)
		}
	}
	out, err := Report(ReportOpts{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "in=1500") {
		t.Errorf("missing aggregated input tokens: %s", out)
	}
	if !strings.Contains(out, "Total:") {
		t.Errorf("missing total line: %s", out)
	}
}

func TestReport_TodayFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	yesterday := time.Now().Add(-25 * time.Hour)
	now := time.Now()
	_ = AppendTo(path, Record{TS: yesterday, SessionID: "old", InputTokens: 100})
	_ = AppendTo(path, Record{TS: now, SessionID: "new", InputTokens: 50})

	out, err := Report(ReportOpts{Path: path, Today: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "old") {
		t.Errorf("--today should exclude yesterday: %s", out)
	}
	if !strings.Contains(out, "new") {
		t.Errorf("--today should include now: %s", out)
	}
}

func TestReport_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	out, err := Report(ReportOpts{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no usage data") {
		t.Errorf("expected friendly empty: %s", out)
	}
}
