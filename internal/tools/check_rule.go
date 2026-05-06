package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/projectfind"
)

// CheckRule pulls a section of rules.md by section heading or a
// keyword search across the body. Useful when the agent needs to
// re-read one rule mid-decision instead of re-scanning the whole file.
type CheckRule struct{}

// NewCheckRule constructs the tool.
func NewCheckRule() *CheckRule { return &CheckRule{} }

// Name implements Tool.
func (CheckRule) Name() string { return "check_rule" }

// Description implements Tool.
func (CheckRule) Description() string {
	return "Look up a section of the project's rules.md by section heading or keyword."
}

// InputSchema implements Tool.
func (CheckRule) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project_root": map[string]any{"type": "string", "description": "Absolute path of the project root"},
			"query":        map[string]any{"type": "string", "description": "Section heading (e.g. 'Deny') or keyword (e.g. 'rm -rf')"},
		},
		"required": []string{"project_root", "query"},
	}
}

type checkRuleInput struct {
	ProjectRoot string `json:"project_root"`
	Query       string `json:"query"`
}

type checkRuleOutput struct {
	Section string `json:"section,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	Match   string `json:"match,omitempty"` // "section" | "keyword" | "none"
	Path    string `json:"path,omitempty"`
}

// Execute implements Tool.
func (c *CheckRule) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in checkRuleInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.ProjectRoot == "" || in.Query == "" {
		return nil, fmt.Errorf("project_root and query are required")
	}
	rulesPath := projectfind.RulesPath(in.ProjectRoot)
	rules, err := config.CachedLoadRules(rulesPath)
	if err != nil {
		return nil, fmt.Errorf("load rules.md: %w", err)
	}

	out := checkRuleOutput{Path: rulesPath}
	if section := findSection(rules.Body, in.Query); section != "" {
		out.Section = sectionHeading(rules.Body, in.Query)
		out.Snippet = section
		out.Match = "section"
	} else if snippet := findKeyword(rules.Body, in.Query); snippet != "" {
		out.Snippet = snippet
		out.Match = "keyword"
	} else {
		out.Match = "none"
	}
	return json.Marshal(out)
}

// findSection extracts the body of a Markdown ## section whose heading
// matches query (case-insensitive substring).
func findSection(body, query string) string {
	lines := strings.Split(body, "\n")
	q := strings.ToLower(query)
	start := -1
	for i, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		head := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		if strings.Contains(head, q) {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for j := start + 1; j < len(lines); j++ {
		if strings.HasPrefix(lines[j], "## ") {
			end = j
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func sectionHeading(body, query string) string {
	lines := strings.Split(body, "\n")
	q := strings.ToLower(query)
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			head := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			if strings.Contains(strings.ToLower(head), q) {
				return head
			}
		}
	}
	return ""
}

// findKeyword returns up to 3 lines of context around the first match.
func findKeyword(body, query string) string {
	lines := strings.Split(body, "\n")
	q := strings.ToLower(query)
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), q) {
			start := i - 1
			if start < 0 {
				start = 0
			}
			end := i + 2
			if end > len(lines) {
				end = len(lines)
			}
			return strings.Join(lines[start:end], "\n")
		}
	}
	return ""
}
