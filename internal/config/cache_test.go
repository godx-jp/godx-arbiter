package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachedLoadRules_HitMiss(t *testing.T) {
	ClearCache()
	path := filepath.Join(t.TempDir(), "rules.md")
	if err := os.WriteFile(path, []byte("body v1"), 0644); err != nil {
		t.Fatal(err)
	}

	r1, err := CachedLoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := CachedLoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if r1 != r2 {
		t.Error("expected same pointer on cache hit")
	}

	// Bump mtime by writing again. Sleep to ensure mtime advances on
	// filesystems with second-resolution timestamps.
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(path, []byte("body v2"), 0644); err != nil {
		t.Fatal(err)
	}
	r3, err := CachedLoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if r3 == r1 {
		t.Error("expected re-parse after mtime change")
	}
	if r3.Body != "body v2" {
		t.Errorf("Body = %q, want body v2", r3.Body)
	}
}

func TestCachedLoadPolicy_HitMiss(t *testing.T) {
	ClearCache()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p1, err := CachedLoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := CachedLoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Error("expected cache hit")
	}
}
