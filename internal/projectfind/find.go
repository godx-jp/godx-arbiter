// Package projectfind discovers a project's .arbiter/ config directory
// by walking up the filesystem from a starting directory.
package projectfind

import (
	"errors"
	"os"
	"path/filepath"
)

// ConfigDirName is the directory containing rules.md, policy.yaml, etc.
const ConfigDirName = ".arbiter"

// ErrNotFound is returned when no .arbiter directory is found by walking up.
var ErrNotFound = errors.New("projectfind: no .arbiter directory found")

// Find walks upward from startDir looking for a directory containing
// .arbiter/. Returns the directory containing .arbiter (the "project root").
//
// Stops at the filesystem root. The returned path is absolute.
func Find(startDir string) (string, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, ConfigDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}

// ConfigDir returns the path to .arbiter/ inside projectRoot.
func ConfigDir(projectRoot string) string {
	return filepath.Join(projectRoot, ConfigDirName)
}

// RulesPath returns the canonical path to rules.md within a project.
func RulesPath(projectRoot string) string {
	return filepath.Join(projectRoot, ConfigDirName, "rules.md")
}

// PolicyPath returns the canonical path to policy.yaml within a project.
func PolicyPath(projectRoot string) string {
	return filepath.Join(projectRoot, ConfigDirName, "policy.yaml")
}
