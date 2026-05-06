package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// RulesFrontMatter holds the optional YAML front matter declared at the
// top of rules.md. All fields are optional — zero values mean "use
// global / built-in default".
//
// See docs/RULES_SPEC.md for the canonical field list.
type RulesFrontMatter struct {
	AgentModel               string   `yaml:"agent_model,omitempty"`
	TimeoutSeconds           int      `yaml:"timeout_seconds,omitempty"`
	OnTimeout                string   `yaml:"on_timeout,omitempty"` // approve|deny|escalate
	OnError                  string   `yaml:"on_error,omitempty"`   // approve|deny
	NotifyChannels           []string `yaml:"notify_channels,omitempty"`
	QuietHours               string   `yaml:"quiet_hours,omitempty"`
	EscalationTimeoutSeconds int      `yaml:"escalation_timeout_seconds,omitempty"`
	MaxAgentIterations       int      `yaml:"max_agent_iterations,omitempty"`
	// Enabled is *bool to distinguish "unset" (nil → default true) from
	// "explicitly false" (kill switch).
	Enabled *bool `yaml:"enabled,omitempty"`
}

// Rules is a parsed rules.md.
type Rules struct {
	Path        string           // absolute path to rules.md
	FrontMatter RulesFrontMatter // parsed YAML front matter (zero if absent)
	Body        string           // markdown body, ready to feed to the agent
	ModTime     time.Time        // file mtime at parse — used for cache invalidation
}

// IsEnabled reports whether arbiter is enabled for this project. Default
// is true; only an explicit `enabled: false` in front matter disables.
func (r *Rules) IsEnabled() bool {
	if r == nil || r.FrontMatter.Enabled == nil {
		return true
	}
	return *r.FrontMatter.Enabled
}

// LoadRules reads and parses rules.md at path. Front matter is optional;
// missing front matter is fine and yields a zero-valued struct.
//
// Returns os.ErrNotExist if the file is missing — callers may treat that
// as "no project rules" rather than a hard failure.
func LoadRules(path string) (*Rules, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	front, body := splitFrontMatter(raw)
	r := &Rules{
		Path:    path,
		Body:    string(body),
		ModTime: info.ModTime(),
	}
	if len(front) > 0 {
		if err := yaml.Unmarshal(front, &r.FrontMatter); err != nil {
			return nil, fmt.Errorf("rules.md front matter: %w", err)
		}
		if err := validateRulesFrontMatter(&r.FrontMatter); err != nil {
			return nil, fmt.Errorf("rules.md front matter: %w", err)
		}
	}
	return r, nil
}

func validateRulesFrontMatter(fm *RulesFrontMatter) error {
	switch fm.OnTimeout {
	case "", "approve", "deny", "escalate":
	default:
		return fmt.Errorf("on_timeout: %q (must be approve|deny|escalate)", fm.OnTimeout)
	}
	switch fm.OnError {
	case "", "approve", "deny":
	default:
		return fmt.Errorf("on_error: %q (must be approve|deny)", fm.OnError)
	}
	if fm.TimeoutSeconds < 0 {
		return fmt.Errorf("timeout_seconds: %d (must be >= 0)", fm.TimeoutSeconds)
	}
	if fm.EscalationTimeoutSeconds < 0 {
		return fmt.Errorf("escalation_timeout_seconds: %d (must be >= 0)", fm.EscalationTimeoutSeconds)
	}
	if fm.MaxAgentIterations < 0 {
		return fmt.Errorf("max_agent_iterations: %d (must be >= 0)", fm.MaxAgentIterations)
	}
	return nil
}
