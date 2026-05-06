package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

func withTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GODX_ARBITER_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
}

func TestGet_EnvVarWins(t *testing.T) {
	withTempHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	if v, _ := Get(ProviderAnthropic); v != "from-env" {
		t.Errorf("got %q", v)
	}
}

func TestSetAndGet_Keychain(t *testing.T) {
	withTempHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	location, err := Set(ProviderAnthropic, "from-keychain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(location, "keychain") {
		t.Errorf("expected keychain location, got %q", location)
	}
	if v, _ := Get(ProviderAnthropic); v != "from-keychain" {
		t.Errorf("got %q", v)
	}
	if err := Delete(ProviderAnthropic); err != nil {
		t.Fatal(err)
	}
	if v, _ := Get(ProviderAnthropic); v != "" {
		t.Errorf("expected empty after delete, got %q", v)
	}
}

func TestFallbackFile_RoundTrip(t *testing.T) {
	withTempHome(t)
	t.Setenv("OPENAI_API_KEY", "")
	path := filepath.Join(os.Getenv("GODX_ARBITER_HOME"), "credentials")
	_ = os.WriteFile(path, []byte("openai=manual\n# comment\n"), 0o600)
	if v, _ := Get(ProviderOpenAI); v != "manual" {
		t.Errorf("got %q", v)
	}
}

func TestList(t *testing.T) {
	withTempHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "x")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GODX_ARBITER_TELEGRAM_TOKEN", "")
	got := List()
	if len(got) != 1 || got[0] != ProviderAnthropic {
		t.Errorf("got %v", got)
	}
}
