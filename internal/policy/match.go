package policy

import (
	"encoding/json"

	"github.com/godx-team/godx-arbiter/internal/config"
)

// matches reports whether a given rule applies to the action. It
// performs the tool match, picks the appropriate field, and runs the
// compiled regex.
func matches(r config.PolicyRule, a *Action) bool {
	if r.Tool != "*" && r.Tool != a.ToolName {
		return false
	}
	if r.Compiled == nil {
		// Tool-only rule. Match.
		return true
	}
	field := r.Field
	if field == "" {
		// Wildcard rule + no explicit field: match against the raw JSON.
		// A wildcard rule by definition can apply to tools whose default
		// field doesn't even exist in the input shape; "raw" is the only
		// thing that's always meaningful.
		if r.Tool == "*" {
			field = "raw"
		} else {
			field = defaultFieldFor(a.ToolName)
		}
	}
	target := selectField(field, a)
	return r.Compiled.MatchString(target)
}

// selectField resolves the value to match against. The special field
// "raw" returns the entire JSON input string. Otherwise it looks up the
// named field on the JSON input map.
//
// If the field is missing or non-string, returns the JSON-stringified
// form (so a regex like `"file_path":\s*"/etc/`) still has something to
// match against). Unknown tools fall through to "raw" upstream.
func selectField(field string, a *Action) string {
	if field == "raw" {
		return string(a.ToolInput)
	}
	if len(a.ToolInput) == 0 {
		return ""
	}
	var input map[string]any
	if err := json.Unmarshal(a.ToolInput, &input); err != nil {
		return string(a.ToolInput)
	}
	v, ok := input[field]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// Non-string: serialize it back so regex authors can match
	// numeric / boolean / nested values when they intentionally do so.
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}

// defaultFieldFor maps known Claude-Code tool names to the field most
// commonly worth matching. Adapters for other CLIs (Codex, Gemini,
// Antigravity) normalize their tool names + input shapes to this
// vocabulary.
func defaultFieldFor(tool string) string {
	switch tool {
	case "Bash":
		return "command"
	case "Read", "Edit", "Write":
		return "file_path"
	case "Glob":
		return "pattern"
	case "Grep":
		return "pattern"
	case "Task":
		return "description"
	default:
		return "raw"
	}
}
