package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/godx-team/godx-arbiter/internal/projectfind"
)

// Project bundles the parsed configuration for a single project.
//
// Either Rules or Policy may be nil — projects that have only one of
// the two files are valid (and common). At least one is required for a
// project to be considered "configured".
type Project struct {
	Root   string  // absolute path to the project root
	Rules  *Rules  // parsed .arbiter/rules.md (nil if absent)
	Policy *Policy // parsed .arbiter/policy.yaml (nil if absent)

	// Warnings accumulates non-fatal issues from loading both files.
	Warnings []string
}

// LoadFromCwd discovers a project by walking up from cwd, then loads
// rules.md and policy.yaml from its .arbiter/ directory.
//
// Returns projectfind.ErrNotFound if no .arbiter/ exists in cwd or any
// ancestor. Callers typically treat that as "use built-in defaults"
// rather than as a hard error.
func LoadFromCwd(cwd string) (*Project, error) {
	root, err := projectfind.Find(cwd)
	if err != nil {
		return nil, err
	}
	return LoadProject(root)
}

// LoadProject loads rules.md and policy.yaml from <root>/.arbiter/.
//
// Missing files are not errors — they yield nil Rules / Policy on the
// returned Project. Hard parse errors on a file that exists are
// returned as a wrapped error.
func LoadProject(root string) (*Project, error) {
	p := &Project{Root: root}

	rulesPath := projectfind.RulesPath(root)
	if r, err := LoadRules(rulesPath); err == nil {
		p.Rules = r
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load rules.md: %w", err)
	}

	policyPath := projectfind.PolicyPath(root)
	if pol, err := LoadPolicy(policyPath); err == nil {
		p.Policy = pol
		p.Warnings = append(p.Warnings, pol.Warnings...)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load policy.yaml: %w", err)
	}

	return p, nil
}

// HasRules reports whether the project has a rules.md.
func (p *Project) HasRules() bool { return p != nil && p.Rules != nil }

// HasPolicy reports whether the project has a policy.yaml.
func (p *Project) HasPolicy() bool { return p != nil && p.Policy != nil }

// IsConfigured reports whether at least one of rules.md or policy.yaml
// is present.
func (p *Project) IsConfigured() bool { return p.HasRules() || p.HasPolicy() }
