package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_BuiltInLibrary(t *testing.T) {
	root := t.TempDir()
	rules := "# rules\n\nblah\n\n@include skill:safe-bash-allowlist\n@include skill:review-before-merge\n"
	bodies, err := Resolve(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("got %d bodies, want 2", len(bodies))
	}
	if !strings.Contains(bodies[0], "safe-bash-allowlist") {
		t.Errorf("first body missing skill label")
	}
}

func TestResolve_ProjectOverridesLibrary(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".arbiter", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "safe-bash-allowlist.md"),
		[]byte("PROJECT-LOCAL OVERRIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	rules := "@include skill:safe-bash-allowlist\n"
	bodies, err := Resolve(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("got %d bodies", len(bodies))
	}
	if !strings.Contains(bodies[0], "PROJECT-LOCAL OVERRIDE") {
		t.Errorf("project skill did not win: %s", bodies[0])
	}
}

func TestResolve_MissingSkillIsSkipped(t *testing.T) {
	rules := "@include skill:does-not-exist\n@include skill:secret-scanning\n"
	bodies, err := Resolve(t.TempDir(), rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("got %d bodies, want 1 (only the existing skill)", len(bodies))
	}
}

func TestResolve_NoIncludes(t *testing.T) {
	bodies, err := Resolve(t.TempDir(), "no includes here")
	if err != nil {
		t.Fatal(err)
	}
	if bodies != nil {
		t.Errorf("got %v", bodies)
	}
}
