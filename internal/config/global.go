package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// GlobalConfig is the per-user arbiter config. Lives at
//
//	$GODX_ARBITER_HOME/config.yaml
//	or $XDG_CONFIG_HOME/godx-arbiter/config.yaml
//	or ~/.config/godx-arbiter/config.yaml
//
// Holds defaults that are too workstation-specific to live in a
// project's .arbiter/ directory: the proxy port, fallback rules.md
// pointer, per-CLI mode, default notification channels.
type GlobalConfig struct {
	Path string `yaml:"-"`

	Proxy        GlobalProxy        `yaml:"proxy,omitempty"`
	FallbackRules string             `yaml:"fallback_rules,omitempty"`
	CLIs         map[string]GlobalCLI `yaml:"clis,omitempty"`
	Notify       GlobalNotify       `yaml:"notify,omitempty"`
}

// GlobalProxy configures the LLM proxy server.
type GlobalProxy struct {
	Addr  string `yaml:"addr,omitempty"`  // e.g. ":7777"
	Anthropic string `yaml:"anthropic,omitempty"` // override upstream URL
	OpenAI    string `yaml:"openai,omitempty"`
	Gemini    string `yaml:"gemini,omitempty"`
}

// GlobalCLI is the per-CLI block from MULTI_CLI.md.
type GlobalCLI struct {
	Mode          string         `yaml:"mode,omitempty"` // hook | proxy | hybrid
	ProxyEndpoint string         `yaml:"proxy_endpoint,omitempty"`
	APIKeyEnv     string         `yaml:"api_key_env,omitempty"`
	Hooks         map[string]bool `yaml:"hooks,omitempty"`
	MCPRegister   *bool          `yaml:"mcp_register,omitempty"`
}

// GlobalNotify holds the default notification preferences when a
// project has no rules.md to override.
type GlobalNotify struct {
	Channels    []string `yaml:"channels,omitempty"`
	QuietHours  string   `yaml:"quiet_hours,omitempty"`
	DedupSecs   int      `yaml:"dedup_secs,omitempty"`
}

// GlobalPath resolves the canonical config.yaml location.
func GlobalPath() string {
	if v := os.Getenv("GODX_ARBITER_HOME"); v != "" {
		return filepath.Join(v, "config.yaml")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "godx-arbiter", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "godx-arbiter", "config.yaml")
}

// LoadGlobal reads the user's config.yaml. A missing file is not an
// error — callers get an empty GlobalConfig and can fall back to
// hard-coded defaults.
func LoadGlobal() (*GlobalConfig, error) {
	return LoadGlobalFrom(GlobalPath())
}

// LoadGlobalFrom is the testable variant.
func LoadGlobalFrom(path string) (*GlobalConfig, error) {
	gc := &GlobalConfig{Path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return gc, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(raw, gc); err != nil {
		return nil, err
	}
	gc.Path = path
	return gc, nil
}

// SaveGlobal writes the config back to its origin path. Used by future
// `arbiter init --global` flows; today we ship a hand-edited file.
func (g *GlobalConfig) Save() error {
	if g.Path == "" {
		g.Path = GlobalPath()
	}
	if err := os.MkdirAll(filepath.Dir(g.Path), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(g)
	if err != nil {
		return err
	}
	return os.WriteFile(g.Path, out, 0o644)
}

// FallbackRulesBody loads the file pointed to by FallbackRules, if any.
// Returns ("", nil) when no fallback is configured or the file is
// missing — projects without a rules.md still work without one.
func (g *GlobalConfig) FallbackRulesBody() (string, error) {
	if g == nil || g.FallbackRules == "" {
		return "", nil
	}
	path := g.FallbackRules
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(g.Path), path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(raw), nil
}
