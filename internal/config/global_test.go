package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGlobal_Missing(t *testing.T) {
	g, err := LoadGlobalFrom(filepath.Join(t.TempDir(), "no.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Proxy.Addr != "" {
		t.Errorf("expected empty defaults, got %+v", g)
	}
}

func TestLoadGlobal_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
proxy:
  addr: ":7777"
fallback_rules: ./fallback-rules.md
clis:
  claude-code:
    mode: hook
  codex:
    mode: proxy
    proxy_endpoint: http://localhost:7777/v1
notify:
  channels: [telegram, desktop]
  quiet_hours: "22:00-07:00"
  dedup_secs: 60
`), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobalFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Proxy.Addr != ":7777" {
		t.Errorf("addr = %q", g.Proxy.Addr)
	}
	if g.CLIs["codex"].Mode != "proxy" {
		t.Errorf("codex mode = %q", g.CLIs["codex"].Mode)
	}
	if len(g.Notify.Channels) != 2 {
		t.Errorf("channels = %v", g.Notify.Channels)
	}
}

func TestFallbackRulesBody(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "fallback-rules.md")
	if err := os.WriteFile(rulesPath, []byte("# fallback\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`fallback_rules: fallback-rules.md`), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobalFrom(configPath)
	if err != nil {
		t.Fatal(err)
	}
	body, err := g.FallbackRulesBody()
	if err != nil {
		t.Fatal(err)
	}
	if body != "# fallback\n" {
		t.Errorf("got %q", body)
	}
}
