package projectfind

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFind_DirectMatch(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, ConfigDirName))

	got, err := Find(tmp)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want, _ := filepath.Abs(tmp)
	if got != want {
		t.Errorf("Find = %q, want %q", got, want)
	}
}

func TestFind_WalksUp(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "project")
	deep := filepath.Join(root, "a", "b", "c")
	mustMkdir(t, deep)
	mustMkdir(t, filepath.Join(root, ConfigDirName))

	got, err := Find(deep)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want, _ := filepath.Abs(root)
	if got != want {
		t.Errorf("Find = %q, want %q", got, want)
	}
}

func TestFind_NotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := Find(tmp)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Find err = %v, want ErrNotFound", err)
	}
}

func TestFind_FileNamedSameAsConfigDir_NotMatched(t *testing.T) {
	tmp := t.TempDir()
	// A file (not a dir) named .arbiter should not match.
	if err := os.WriteFile(filepath.Join(tmp, ConfigDirName), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Find(tmp)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Find err = %v, want ErrNotFound (file should not match)", err)
	}
}

func TestPaths(t *testing.T) {
	root := "/proj"
	if got := ConfigDir(root); got != "/proj/.arbiter" {
		t.Errorf("ConfigDir = %q", got)
	}
	if got := RulesPath(root); got != "/proj/.arbiter/rules.md" {
		t.Errorf("RulesPath = %q", got)
	}
	if got := PolicyPath(root); got != "/proj/.arbiter/policy.yaml" {
		t.Errorf("PolicyPath = %q", got)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
}
