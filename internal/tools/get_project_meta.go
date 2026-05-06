package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GetProjectMeta returns lightweight project context (branch, recent
// commits, presence of CLAUDE.md, detected languages).
type GetProjectMeta struct{}

// NewGetProjectMeta constructs the tool.
func NewGetProjectMeta() *GetProjectMeta { return &GetProjectMeta{} }

// Name implements Tool.
func (GetProjectMeta) Name() string { return "get_project_meta" }

// Description implements Tool.
func (GetProjectMeta) Description() string {
	return "Project context: name, branch, recent commits, CLAUDE.md presence, detected languages."
}

// InputSchema implements Tool.
func (GetProjectMeta) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project_root": map[string]any{"type": "string"},
		},
		"required": []string{"project_root"},
	}
}

type projectMetaIn struct {
	ProjectRoot string `json:"project_root"`
}

type projectMetaOut struct {
	Name          string   `json:"name"`
	Branch        string   `json:"branch,omitempty"`
	RecentCommits []string `json:"recent_commits,omitempty"`
	HasClaudeMD   bool     `json:"has_claude_md"`
	HasArbiter    bool     `json:"has_arbiter"`
	Languages     []string `json:"languages,omitempty"`
	GitDirty      bool     `json:"git_dirty,omitempty"`
}

// Execute implements Tool.
func (g *GetProjectMeta) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in projectMetaIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(in.ProjectRoot)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("project root %q: %w", root, err)
	}

	out := projectMetaOut{
		Name:        filepath.Base(root),
		HasClaudeMD: existsFile(filepath.Join(root, "CLAUDE.md")),
		HasArbiter:  existsDir(filepath.Join(root, ".arbiter")),
		Languages:   detectLanguages(root),
	}

	if branch, err := gitOutput(ctx, root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		out.Branch = strings.TrimSpace(branch)
	}
	if commits, err := gitOutput(ctx, root, "log", "--oneline", "-5"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(commits), "\n") {
			if line != "" {
				out.RecentCommits = append(out.RecentCommits, line)
			}
		}
	}
	if status, err := gitOutput(ctx, root, "status", "--porcelain"); err == nil {
		out.GitDirty = strings.TrimSpace(status) != ""
	}

	return json.Marshal(out)
}

func existsFile(path string) bool {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return true
	}
	return false
}

func existsDir(path string) bool {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return true
	}
	return false
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func detectLanguages(root string) []string {
	hits := map[string]bool{}
	checks := map[string][]string{
		"go":         {"go.mod"},
		"typescript": {"tsconfig.json", "package.json"},
		"javascript": {"package.json"},
		"python":     {"pyproject.toml", "requirements.txt", "setup.py"},
		"rust":       {"Cargo.toml"},
		"java":       {"pom.xml", "build.gradle", "build.gradle.kts"},
	}
	for lang, files := range checks {
		for _, f := range files {
			if existsFile(filepath.Join(root, f)) {
				hits[lang] = true
				break
			}
		}
	}
	out := make([]string, 0, len(hits))
	for k := range hits {
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stdlibSentinel keeps go vet from flagging unused imports if all
// callers compile out (defensive — currently every import is used).
var _ = errors.New
