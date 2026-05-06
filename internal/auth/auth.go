// Package auth resolves provider API keys from a precedence chain:
//
//	1. Process env var (e.g. ANTHROPIC_API_KEY).
//	2. OS keychain entry under service "godx-arbiter".
//	3. Optional plaintext fallback file at $GODX_ARBITER_HOME/credentials.
//
// The keychain is the recommended store; env vars are kept for parity
// with the upstream SDKs (Claude Code, Codex CLI etc. all expect the
// vendor's standard env var). The plaintext fallback exists for headless
// environments (CI containers, throwaway VMs) where no keychain is
// available; we warn loudly when it's used.
package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const ServiceName = "godx-arbiter"

// Provider names a credentialed backend.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderGoogle    Provider = "google"
	ProviderTelegram  Provider = "telegram"
)

// EnvVar returns the canonical environment variable name for a provider.
func (p Provider) EnvVar() string {
	switch p {
	case ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case ProviderOpenAI:
		return "OPENAI_API_KEY"
	case ProviderGoogle:
		return "GOOGLE_API_KEY"
	case ProviderTelegram:
		return "GODX_ARBITER_TELEGRAM_TOKEN"
	}
	return ""
}

// Get resolves an API key for the given provider. Returns ("", nil)
// when no credential is configured anywhere — callers decide whether
// that's an error in their context (slow-path agent: yes; fast-path-
// only setup: no).
func Get(p Provider) (string, error) {
	if v := os.Getenv(p.EnvVar()); v != "" {
		return v, nil
	}
	if v, err := keyring.Get(ServiceName, string(p)); err == nil && v != "" {
		return v, nil
	} else if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		// Keychain present but errored (locked, no daemon, etc.).
		// Don't fail — try the plaintext fallback.
		fmt.Fprintf(os.Stderr, "[arbiter auth] keychain lookup failed for %s: %v\n", p, err)
	}
	if v, ok := readFallback(p); ok {
		fmt.Fprintf(os.Stderr, "[arbiter auth] using plaintext credential for %s — consider 'arbiter auth set %s'\n", p, p)
		return v, nil
	}
	return "", nil
}

// Set stores a credential in the OS keychain. Returns the path used so
// callers can show the user where it ended up (or fall back to file
// when keyring isn't available).
func Set(p Provider, value string) (string, error) {
	if value == "" {
		return "", errors.New("auth: empty value")
	}
	if err := keyring.Set(ServiceName, string(p), value); err == nil {
		return "keychain (service=" + ServiceName + ", account=" + string(p) + ")", nil
	} else {
		fmt.Fprintf(os.Stderr, "[arbiter auth] keychain set failed: %v — falling back to file\n", err)
	}
	return writeFallback(p, value)
}

// Delete removes a stored credential. Errors when no entry exists are
// returned so the caller can show the user a clear message.
func Delete(p Provider) error {
	if err := keyring.Delete(ServiceName, string(p)); err == nil {
		return nil
	} else if !errors.Is(err, keyring.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "[arbiter auth] keychain delete: %v\n", err)
	}
	return removeFallback(p)
}

// List returns the providers we have at least one credential for.
// Best-effort — keyring backends generally don't expose enumeration so
// we check each known provider individually.
func List() []Provider {
	var out []Provider
	for _, p := range []Provider{ProviderAnthropic, ProviderOpenAI, ProviderGoogle, ProviderTelegram} {
		if v, _ := Get(p); v != "" {
			out = append(out, p)
		}
	}
	return out
}

// fallbackPath returns ${GODX_ARBITER_HOME:-${XDG_CONFIG_HOME:-~/.config}/godx-arbiter}/credentials
func fallbackPath() string {
	if v := os.Getenv("GODX_ARBITER_HOME"); v != "" {
		return filepath.Join(v, "credentials")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "godx-arbiter", "credentials")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "godx-arbiter", "credentials")
}

func readFallback(p Provider) (string, bool) {
	raw, err := os.ReadFile(fallbackPath())
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			if strings.EqualFold(strings.TrimSpace(k), string(p)) {
				return strings.TrimSpace(v), true
			}
		}
	}
	return "", false
}

func writeFallback(p Provider, value string) (string, error) {
	path := fallbackPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	raw, _ := os.ReadFile(path)
	lines := strings.Split(string(raw), "\n")
	var out []string
	written := false
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}
		if k, _, ok := strings.Cut(stripped, "="); ok && strings.EqualFold(strings.TrimSpace(k), string(p)) {
			out = append(out, fmt.Sprintf("%s=%s", p, value))
			written = true
			continue
		}
		out = append(out, line)
	}
	if !written {
		out = append(out, fmt.Sprintf("%s=%s", p, value))
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func removeFallback(p Provider) error {
	path := fallbackPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var out []string
	removed := false
	for _, line := range strings.Split(string(raw), "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}
		if k, _, ok := strings.Cut(stripped, "="); ok && strings.EqualFold(strings.TrimSpace(k), string(p)) {
			removed = true
			continue
		}
		out = append(out, line)
	}
	if !removed {
		return errors.New("auth: no credential stored for " + string(p))
	}
	if len(out) == 0 {
		return os.Remove(path)
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600)
}
