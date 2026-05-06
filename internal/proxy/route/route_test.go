package route

import (
	"testing"

	"github.com/godx-team/godx-arbiter/internal/proxy/classify"
)

func TestParseAndPick(t *testing.T) {
	body := `# Project rules

Some preamble.

## Model routing

Default models per CLI when no rule below matches:
- Claude Code: claude-sonnet-4-6
- Codex: gpt-5

Rules (top to bottom; first match wins):

- task: read-only-summarization
  model: claude-haiku-4-5-20251001
  reason: cheap

- task: hard-reasoning
  model: claude-opus-4-7

## Other section

unrelated.
`
	tbl := ParseSection(body)
	if tbl.Empty() {
		t.Fatal("table is empty")
	}
	if got, _ := tbl.Pick("claude-code", "claude-sonnet-4-6", classify.TagReadOnlySummarization); got != "claude-haiku-4-5-20251001" {
		t.Errorf("got %q", got)
	}
	if got, _ := tbl.Pick("claude-code", "x", classify.TagHardReasoning); got != "claude-opus-4-7" {
		t.Errorf("hard-reasoning got %q", got)
	}
	// No matching rule → default
	if got, reason := tbl.Pick("claude-code", "x", classify.TagOther); got != "claude-sonnet-4-6" || reason != "cli default" {
		t.Errorf("default fallback got %q reason=%q", got, reason)
	}
	if got, _ := tbl.Pick("codex", "gpt-5", classify.TagOther); got != "gpt-5" {
		t.Errorf("codex default got %q", got)
	}
}

func TestParseSection_Missing(t *testing.T) {
	tbl := ParseSection("# rules\n\nno routing section here")
	if !tbl.Empty() {
		t.Errorf("expected empty table")
	}
}
