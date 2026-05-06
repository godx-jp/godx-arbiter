package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPolicy_Full(t *testing.T) {
	content := `version: 1
default: agent

deny:
  - tool: Bash
    pattern: '\brm\s+-rf\b'
    reason: destructive

allow:
  - tool: Read
  - tool: Bash
    pattern: '^(ls|cat)\b'
    reason: read-only

to_agent:
  - tool: Edit
    field: file_path
    pattern: 'backend/internal/config/'
    reason: deployment-critical
`
	p := loadPolicyString(t, content)
	if p.Default != "agent" {
		t.Errorf("Default = %q", p.Default)
	}
	if len(p.Deny) != 1 {
		t.Errorf("Deny count = %d", len(p.Deny))
	}
	if len(p.Allow) != 2 {
		t.Errorf("Allow count = %d", len(p.Allow))
	}
	if len(p.ToAgent) != 1 {
		t.Errorf("ToAgent count = %d", len(p.ToAgent))
	}
	// Compiled regexes present where pattern given
	if p.Deny[0].Compiled == nil {
		t.Error("Deny[0] regex not compiled")
	}
	if p.Allow[0].Compiled != nil {
		t.Error("Allow[0] should have nil regex (no pattern)")
	}
	if !p.Deny[0].Compiled.MatchString("rm -rf /tmp") {
		t.Error("Deny regex does not match expected input")
	}
	if len(p.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", p.Warnings)
	}
}

func TestLoadPolicy_DefaultsToAgent(t *testing.T) {
	content := `version: 1
allow:
  - tool: Read
`
	p := loadPolicyString(t, content)
	if p.Default != DefaultAgent {
		t.Errorf("Default = %q, want %q", p.Default, DefaultAgent)
	}
}

func TestLoadPolicy_MissingVersion(t *testing.T) {
	content := `allow:
  - tool: Read
`
	if _, err := tryLoadPolicyString(t, content); err == nil {
		t.Error("expected error for missing version")
	}
}

func TestLoadPolicy_BadVersion(t *testing.T) {
	content := `version: 99
`
	_, err := tryLoadPolicyString(t, content)
	if err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadPolicy_BadDefault(t *testing.T) {
	content := `version: 1
default: something_weird
`
	if _, err := tryLoadPolicyString(t, content); err == nil {
		t.Error("expected error for bad default")
	}
}

func TestLoadPolicy_BadRegexCollectsWarning(t *testing.T) {
	content := `version: 1
deny:
  - tool: Bash
    pattern: '['        # invalid regex
    reason: bad
allow:
  - tool: Read          # this should still load
`
	p := loadPolicyString(t, content)
	if len(p.Warnings) == 0 {
		t.Error("expected warning for bad regex")
	}
	if len(p.Deny) != 0 {
		t.Errorf("bad rule should be dropped, got %d", len(p.Deny))
	}
	if len(p.Allow) != 1 {
		t.Errorf("good rule should remain, got %d", len(p.Allow))
	}
}

func TestLoadPolicy_MissingToolDropped(t *testing.T) {
	content := `version: 1
deny:
  - pattern: 'something'
`
	p := loadPolicyString(t, content)
	if len(p.Deny) != 0 {
		t.Errorf("rule with no tool should be dropped, got %d", len(p.Deny))
	}
	if len(p.Warnings) == 0 {
		t.Error("expected warning")
	}
}

func TestLoadPolicy_FileMissing(t *testing.T) {
	tmp := t.TempDir()
	_, err := LoadPolicy(filepath.Join(tmp, "nope.yaml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want ErrNotExist", err)
	}
}

func loadPolicyString(t *testing.T, content string) *Policy {
	t.Helper()
	p, err := tryLoadPolicyString(t, content)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	return p
}

func tryLoadPolicyString(t *testing.T, content string) (*Policy, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return LoadPolicy(path)
}
