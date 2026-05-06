package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/godx-team/godx-arbiter/internal/projectfind"
)

func TestLoadFromCwd_FullProject(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, projectfind.ConfigDirName)
	if err := os.MkdirAll(cfg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "rules.md"), []byte("# Rules\nbody"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "policy.yaml"), []byte("version: 1\nallow:\n  - tool: Read\n"), 0644); err != nil {
		t.Fatal(err)
	}

	subdir := filepath.Join(root, "deep", "nested")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	p, err := LoadFromCwd(subdir)
	if err != nil {
		t.Fatalf("LoadFromCwd: %v", err)
	}
	wantRoot, _ := filepath.Abs(root)
	if p.Root != wantRoot {
		t.Errorf("Root = %q, want %q", p.Root, wantRoot)
	}
	if !p.HasRules() {
		t.Error("expected rules loaded")
	}
	if !p.HasPolicy() {
		t.Error("expected policy loaded")
	}
	if !p.IsConfigured() {
		t.Error("expected configured")
	}
}

func TestLoadFromCwd_RulesOnly(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, projectfind.ConfigDirName)
	if err := os.MkdirAll(cfg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "rules.md"), []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadFromCwd(root)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasRules() || p.HasPolicy() {
		t.Errorf("HasRules=%v HasPolicy=%v", p.HasRules(), p.HasPolicy())
	}
}

func TestLoadFromCwd_NoArbiter(t *testing.T) {
	root := t.TempDir()
	_, err := LoadFromCwd(root)
	if !errors.Is(err, projectfind.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLoadFromCwd_BadPolicyPropagates(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, projectfind.ConfigDirName)
	if err := os.MkdirAll(cfg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "policy.yaml"), []byte("version: 99\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadFromCwd(root); err == nil {
		t.Error("expected error for unsupported policy version")
	}
}
