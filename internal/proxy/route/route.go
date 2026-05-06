// Package route applies rules.md `## Model routing` rules to map a
// task tag → target model. The rule format is intentionally simple: a
// list of {tag, model, reason} entries; first match wins.
//
// The parser tolerates Markdown-formatted sections (the same source
// rules.md uses) so the user doesn't have to maintain a separate file.
package route

import (
	"regexp"
	"strings"

	"github.com/godx-team/godx-arbiter/internal/proxy/classify"
)

// Rule maps a task tag to a target model.
type Rule struct {
	Tag    classify.Tag
	Model  string
	Reason string
}

// Table is the parsed routing rules.
type Table struct {
	Defaults map[string]string // cli → default model
	Rules    []Rule
}

// Empty returns true if the table has no rules + no defaults.
func (t *Table) Empty() bool {
	return t == nil || (len(t.Defaults) == 0 && len(t.Rules) == 0)
}

// Pick returns the model to use for a request, falling back to the
// CLI default + the originally-requested model if no rule matches.
func (t *Table) Pick(cli string, requested string, tag classify.Tag) (string, string) {
	if t == nil {
		return requested, ""
	}
	for _, r := range t.Rules {
		if r.Tag == tag {
			return r.Model, r.Reason
		}
	}
	if d, ok := t.Defaults[cli]; ok && d != "" {
		return d, "cli default"
	}
	return requested, ""
}

// ParseSection scans a rules.md body for the `## Model routing` section
// and parses its rule list. A missing section yields an empty Table.
//
// Format:
//
//	## Model routing
//
//	Default models per CLI when no rule below matches:
//	- Claude Code: claude-sonnet-4-6
//	- Codex: gpt-5
//
//	Rules (top to bottom; first match wins):
//
//	- task: read-only-summarization
//	  model: claude-haiku-4-5-20251001
//	  reason: cheap, fast enough for log/diff summaries
//
//	- task: simple-edit
//	  model: claude-haiku-4-5-20251001
//
// We don't do full YAML; just a pragmatic line scan.
func ParseSection(rulesBody string) *Table {
	section := extractSection(rulesBody, "model routing")
	if section == "" {
		return &Table{Defaults: map[string]string{}}
	}

	t := &Table{Defaults: map[string]string{}}
	lines := strings.Split(section, "\n")
	parsingDefaults := false
	parsingRules := false
	var current Rule

	flush := func() {
		if current.Tag != "" && current.Model != "" {
			t.Rules = append(t.Rules, current)
		}
		current = Rule{}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case strings.Contains(strings.ToLower(line), "default models per cli"):
			parsingDefaults = true
			parsingRules = false
			continue
		case strings.Contains(strings.ToLower(line), "rules"):
			flush()
			parsingDefaults = false
			parsingRules = true
			continue
		}
		if parsingDefaults && strings.HasPrefix(line, "- ") {
			if k, v, ok := splitColon(strings.TrimPrefix(line, "- ")); ok {
				t.Defaults[normalizeCLI(k)] = v
			}
			continue
		}
		if parsingRules {
			if strings.HasPrefix(line, "- task:") {
				flush()
				current.Tag = classify.Tag(strings.TrimSpace(strings.TrimPrefix(line, "- task:")))
				continue
			}
			if strings.HasPrefix(line, "model:") {
				current.Model = strings.TrimSpace(strings.TrimPrefix(line, "model:"))
				continue
			}
			if strings.HasPrefix(line, "reason:") {
				current.Reason = strings.TrimSpace(strings.TrimPrefix(line, "reason:"))
				continue
			}
		}
	}
	flush()
	return t
}

func splitColon(s string) (string, string, bool) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:]), true
}

func normalizeCLI(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "claude code", "claudecode":
		return "claude-code"
	}
	return s
}

func extractSection(body, want string) string {
	rx := regexp.MustCompile(`(?m)^## .+$`)
	matches := rx.FindAllStringIndex(body, -1)
	for i, m := range matches {
		header := strings.ToLower(body[m[0]:m[1]])
		if !strings.Contains(header, want) {
			continue
		}
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		return body[m[1]:end]
	}
	return ""
}
