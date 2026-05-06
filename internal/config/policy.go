package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// PolicyVersion is the only currently-supported schema version. See
// docs/POLICY_SPEC.md for the schema.
const PolicyVersion = 1

// PolicyDefault values. The default if unset is DefaultAgent: fall
// through to the slow-path LLM agent for any non-matching tool call.
const (
	DefaultAgent   = "agent"
	DefaultApprove = "approve"
	DefaultDeny    = "deny"
)

// PolicyRule is a single regex-based fast-path rule.
type PolicyRule struct {
	Tool       string `yaml:"tool"`
	Pattern    string `yaml:"pattern,omitempty"`
	Field      string `yaml:"field,omitempty"`
	Reason     string `yaml:"reason,omitempty"`
	Confidence string `yaml:"confidence,omitempty"`

	// Compiled is the compiled regex. Nil means the rule has no
	// pattern (matches all calls of `Tool`).
	Compiled *regexp.Regexp `yaml:"-"`
}

// Policy is a parsed policy.yaml. Rules are evaluated in this order:
// Deny first, then Allow, then ToAgent. Within each list,
// top-to-bottom; first match wins.
type Policy struct {
	Version int          `yaml:"version"`
	Default string       `yaml:"default,omitempty"`
	Allow   []PolicyRule `yaml:"allow,omitempty"`
	Deny    []PolicyRule `yaml:"deny,omitempty"`
	ToAgent []PolicyRule `yaml:"to_agent,omitempty"`

	Path    string    `yaml:"-"`
	ModTime time.Time `yaml:"-"`

	// Warnings are non-fatal validation issues — e.g., a rule with an
	// invalid regex was skipped. Surfaced by `arbiter doctor`.
	Warnings []string `yaml:"-"`
}

// LoadPolicy reads and parses policy.yaml at path.
//
// Returns os.ErrNotExist if missing (callers may treat as "no fast-path,
// always slow-path").
//
// Schema-level errors are returned as a hard error; rule-level issues
// (e.g. invalid regex on one rule) are collected as Warnings on the
// returned Policy and the offending rule is dropped.
func LoadPolicy(path string) (*Policy, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var p Policy
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("policy.yaml: %w", err)
	}
	p.Path = path
	p.ModTime = info.ModTime()

	if err := validateAndCompile(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func validateAndCompile(p *Policy) error {
	if p.Version != PolicyVersion {
		if p.Version == 0 {
			return errors.New("policy.yaml: missing required field `version`")
		}
		return fmt.Errorf("policy.yaml: unsupported version %d (this build supports %d)", p.Version, PolicyVersion)
	}
	switch p.Default {
	case "", DefaultAgent, DefaultApprove, DefaultDeny:
		if p.Default == "" {
			p.Default = DefaultAgent
		}
	default:
		return fmt.Errorf("policy.yaml: invalid default %q (must be agent|approve|deny)", p.Default)
	}

	p.Deny = compileList("deny", p.Deny, &p.Warnings)
	p.Allow = compileList("allow", p.Allow, &p.Warnings)
	p.ToAgent = compileList("to_agent", p.ToAgent, &p.Warnings)

	return nil
}

// compileList compiles regexes and validates required fields. Rules
// with bad regex or missing tool are dropped with a warning.
func compileList(label string, rules []PolicyRule, warnings *[]string) []PolicyRule {
	out := make([]PolicyRule, 0, len(rules))
	for i, r := range rules {
		if r.Tool == "" {
			*warnings = append(*warnings, fmt.Sprintf("%s[%d]: missing `tool` — rule dropped", label, i))
			continue
		}
		if r.Pattern != "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				*warnings = append(*warnings, fmt.Sprintf("%s[%d] (tool=%s): invalid regex %q: %v — rule dropped", label, i, r.Tool, r.Pattern, err))
				continue
			}
			r.Compiled = re
		}
		out = append(out, r)
	}
	return out
}
