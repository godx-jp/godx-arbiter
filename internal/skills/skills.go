// Package skills loads reusable Markdown chunks ("skills") referenced by
// rules.md via a `@include skill:<name>` directive.
//
// Resolution order (first-match wins):
//
//  1. <project>/.arbiter/skills/<name>.md
//  2. $GODX_ARBITER_HOME/skills/<name>.md
//  3. $XDG_CONFIG_HOME/godx-arbiter/skills/<name>.md
//  4. ~/.config/godx-arbiter/skills/<name>.md
//  5. The built-in library (Library map below).
//
// The agent treats each skill as additional system-prompt context. Bad
// skills (file unreadable, missing) are reported on stderr but do not
// abort the decision — we'd rather have a partial system prompt than no
// decision at all (ADR-005 spirit).
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/godx-team/godx-arbiter/internal/projectfind"
)

// Library is the built-in skill set bundled with the binary.
//
// Keep these short — they pile into every agent invocation that
// includes them. Token budget is real even with caching.
var Library = map[string]string{
	"safe-bash-allowlist":   safeBashAllowlist,
	"review-before-merge":   reviewBeforeMerge,
	"test-before-deploy":    testBeforeDeploy,
	"migration-discipline":  migrationDiscipline,
	"secret-scanning":       secretScanning,
}

var includeRx = regexp.MustCompile(`(?m)^[\t ]*@include[\t ]+skill:[\t ]*([A-Za-z0-9_\-./]+)[\t ]*$`)

// Resolve scans rulesBody for `@include skill:<name>` directives and
// returns the resolved skill bodies, in the order they appear. Missing
// skills are skipped with a warning written to stderr.
func Resolve(projectRoot, rulesBody string) ([]string, error) {
	matches := includeRx.FindAllStringSubmatch(rulesBody, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		body, err := Load(projectRoot, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[arbiter] skill %q: %v\n", name, err)
			continue
		}
		out = append(out, fmt.Sprintf("# Skill: %s\n%s", name, body))
	}
	return out, nil
}

// Load returns the body of a skill, searched in the resolution order
// described in the package doc.
func Load(projectRoot, name string) (string, error) {
	for _, dir := range searchDirs(projectRoot) {
		path := filepath.Join(dir, name+".md")
		if data, err := os.ReadFile(path); err == nil {
			return string(data), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if body, ok := Library[name]; ok {
		return body, nil
	}
	return "", fmt.Errorf("skill not found: %s", name)
}

// searchDirs returns the ordered list of skill directories to inspect.
func searchDirs(projectRoot string) []string {
	var dirs []string
	if projectRoot != "" {
		dirs = append(dirs, filepath.Join(projectfind.ConfigDir(projectRoot), "skills"))
	}
	if v := os.Getenv("GODX_ARBITER_HOME"); v != "" {
		dirs = append(dirs, filepath.Join(v, "skills"))
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		dirs = append(dirs, filepath.Join(v, "godx-arbiter", "skills"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "godx-arbiter", "skills"))
	}
	return dirs
}

const safeBashAllowlist = `Read-only or near-zero-risk Bash commands. The agent may approve calls
matching these without further analysis:

- ls, cat, head, tail, wc, stat, file, which, whereis, type, pwd, env, date, hostname, whoami, id
- grep, rg, fd, find, locate, ack, ag
- git status, git log, git diff, git show, git branch, git remote, git config --get, git rev-parse
- go test, go vet, go build, go fmt
- npm test, npm run build, npm run lint
- pnpm test, yarn test, cargo test, make test
`

const reviewBeforeMerge = `Pre-merge checklist. When the action involves merging, pushing, or
finalizing work, the agent must verify:

1. Tests for changed code paths exist and pass.
2. No secret-shaped strings introduced (API keys, tokens, passwords).
3. No literal IPs / hostnames / file paths added in production code.
4. No "// CRITICAL", "// FROZEN", or "// DO NOT EDIT" comments removed.
5. Changes affect <= 10 files; otherwise escalate.
6. Migration files only added, never modified in place.
`

const testBeforeDeploy = `Before any deployment-touching action, the agent should:

- Confirm tests pass (look for evidence in recent_actions or escalate to
  ask).
- Confirm no uncommitted changes outside the deploy artifact path.
- Confirm the deploy target matches rules.md's allowed environments.
`

const migrationDiscipline = `Database migrations are append-only. The agent must DENY:

- Any modification to a migration file whose timestamp is older than
  today.
- Any rename or delete of a migration file.

Escalate when adding a new migration, so the user confirms intent.
`

const secretScanning = `Detect secret-shaped content in proposed file edits / writes.

Flags (case-insensitive):

- AWS_*, GH_*, GITHUB_TOKEN, ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_*
- "ghp_<24+ chars>", "sk-<20+ chars>", "AKIA[0-9A-Z]{16}"
- 32+ char base64 strings near the words "secret", "token", "key",
  "password", "pwd"

When matched, the agent must DENY (do not escalate — keeps secrets out
of notification channels).
`
