package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRules_Full(t *testing.T) {
	content := `---
agent_model: claude-haiku-4-5-20251001
timeout_seconds: 30
on_timeout: deny
on_error: approve
notify_channels: [telegram, desktop]
quiet_hours: "22:00-07:00"
escalation_timeout_seconds: 60
max_agent_iterations: 8
---
# Rules

Some markdown body here.
`
	path := writeTempFile(t, "rules.md", content)
	r, err := LoadRules(path)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if r.FrontMatter.AgentModel != "claude-haiku-4-5-20251001" {
		t.Errorf("AgentModel = %q", r.FrontMatter.AgentModel)
	}
	if r.FrontMatter.TimeoutSeconds != 30 {
		t.Errorf("TimeoutSeconds = %d", r.FrontMatter.TimeoutSeconds)
	}
	if r.FrontMatter.OnTimeout != "deny" {
		t.Errorf("OnTimeout = %q", r.FrontMatter.OnTimeout)
	}
	if len(r.FrontMatter.NotifyChannels) != 2 {
		t.Errorf("NotifyChannels = %v", r.FrontMatter.NotifyChannels)
	}
	if !strings.Contains(r.Body, "Some markdown body") {
		t.Errorf("Body missing markdown content: %q", r.Body)
	}
	if !r.IsEnabled() {
		t.Error("expected enabled by default")
	}
}

func TestLoadRules_NoFrontMatter(t *testing.T) {
	content := `# Plain Markdown Only

No front matter here at all.
`
	path := writeTempFile(t, "rules.md", content)
	r, err := LoadRules(path)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if r.FrontMatter.AgentModel != "" {
		t.Errorf("AgentModel should be empty, got %q", r.FrontMatter.AgentModel)
	}
	if !strings.Contains(r.Body, "Plain Markdown Only") {
		t.Errorf("Body lost: %q", r.Body)
	}
}

func TestLoadRules_DisabledKillSwitch(t *testing.T) {
	content := `---
enabled: false
---
body
`
	path := writeTempFile(t, "rules.md", content)
	r, err := LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.IsEnabled() {
		t.Error("expected disabled")
	}
}

func TestLoadRules_InvalidOnTimeout(t *testing.T) {
	content := `---
on_timeout: maybe
---
body
`
	path := writeTempFile(t, "rules.md", content)
	if _, err := LoadRules(path); err == nil {
		t.Error("expected error for invalid on_timeout")
	}
}

func TestLoadRules_InvalidOnError(t *testing.T) {
	content := `---
on_error: panic
---
body
`
	path := writeTempFile(t, "rules.md", content)
	if _, err := LoadRules(path); err == nil {
		t.Error("expected error for invalid on_error")
	}
}

func TestLoadRules_NegativeTimeout(t *testing.T) {
	content := `---
timeout_seconds: -1
---
body
`
	path := writeTempFile(t, "rules.md", content)
	if _, err := LoadRules(path); err == nil {
		t.Error("expected error for negative timeout")
	}
}

func TestLoadRules_Missing(t *testing.T) {
	tmp := t.TempDir()
	_, err := LoadRules(filepath.Join(tmp, "nope.md"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want ErrNotExist", err)
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}
