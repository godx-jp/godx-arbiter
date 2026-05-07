package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godx-team/godx-arbiter/internal/projectfind"
)

// runInit scaffolds .arbiter/ in the current project and registers
// hooks in ~/.claude/settings.json so a fresh `arbiter init` is enough
// to start coordinating Claude Code sessions.
//
// Two modes:
//
//   - --interactive (default when stdin is a TTY): a wizard asks about
//     the project + risk tolerance + escalation channels and writes a
//     personalized rules.md.
//   - --non-interactive (or piped stdin): falls back to one of the
//     three canned templates (balanced / strict / sandbox).
//
// Per ROADMAP open question #5: refuse to overwrite an existing
// rules.md / policy.yaml unless --force is passed.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	template := fs.String("template", "", "rules.md template: balanced | strict | sandbox (overrides interactive)")
	force := fs.Bool("force", false, "overwrite existing rules.md / policy.yaml")
	skipHooks := fs.Bool("skip-hooks", false, "skip writing ~/.claude/settings.json")
	skipMCP := fs.Bool("skip-mcp", false, "skip MCP server registration in settings.json")
	dir := fs.String("dir", ".", "project root to scaffold (default: cwd)")
	interactive := fs.Bool("interactive", true, "ask interactive questions to personalize rules.md")
	nonInteractive := fs.Bool("non-interactive", false, "skip the wizard; use --template (or 'balanced')")
	_ = fs.Parse(args)

	root, err := filepath.Abs(*dir)
	if err != nil {
		fail("init: resolve dir: %v", err)
	}

	// Wizard activates when stdin is a real TTY (so piped CI input
	// falls back to the template). GODX_ARBITER_FORCE_WIZARD lifts the
	// TTY check for scripted runs and integration tests.
	stdinReady := isTerminal(os.Stdin) || os.Getenv("GODX_ARBITER_FORCE_WIZARD") == "1"
	useWizard := *interactive && !*nonInteractive && *template == "" && stdinReady
	chosenTemplate := *template
	if chosenTemplate == "" {
		chosenTemplate = "balanced"
	}

	var personalizedRules string
	var personalizedPolicy string
	var derivedChannels []string
	if useWizard {
		ans, err := runInitWizard(root)
		if err != nil {
			fail("init wizard: %v", err)
		}
		personalizedRules = ans.RulesBody
		personalizedPolicy = ans.PolicyBody
		derivedChannels = ans.NotifyChannels
		chosenTemplate = ans.TemplateLabel
	}

	if err := scaffoldArbiterDirWithBody(root, chosenTemplate, *force, personalizedRules, personalizedPolicy); err != nil {
		fail("init: scaffold .arbiter: %v", err)
	}

	if !*skipHooks {
		if err := writeClaudeSettings(*skipMCP); err != nil {
			fail("init: claude settings: %v", err)
		}
	}

	fmt.Printf("\n✓ arbiter initialized at %s\n", root)
	fmt.Printf("  template: %s%s\n", chosenTemplate, ifelse(useWizard, " (personalized)", ""))
	if len(derivedChannels) > 0 {
		fmt.Printf("  channels: %s\n", strings.Join(derivedChannels, ", "))
	}
	fmt.Printf("  next steps:\n")
	fmt.Printf("    1. arbiter auth set anthropic    # store API key in OS keychain\n")
	fmt.Printf("    2. arbiter doctor                # verify everything is wired up\n")
	fmt.Printf("    3. open %s/rules.md to refine the rules\n",
		filepath.Join(root, projectfind.ConfigDirName))
}

func scaffoldArbiterDir(root, template string, force bool) error {
	return scaffoldArbiterDirWithBody(root, template, force, "", "")
}

// scaffoldArbiterDirWithBody is the wizard-aware variant — when
// rulesBody / policyBody are non-empty, they're used verbatim
// instead of the canned template.
func scaffoldArbiterDirWithBody(root, template string, force bool, rulesBody, policyBody string) error {
	dir := projectfind.ConfigDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	rulesPath := projectfind.RulesPath(root)
	rules := rulesBody
	if rules == "" {
		rules = rulesTemplate(template)
	}
	if err := writeIfMissing(rulesPath, rules, force); err != nil {
		return err
	}

	policyPath := projectfind.PolicyPath(root)
	policy := policyBody
	if policy == "" {
		policy = policyTemplate(template)
	}
	if err := writeIfMissing(policyPath, policy, force); err != nil {
		return err
	}

	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return err
	}

	return nil
}

func writeIfMissing(path, content string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		fmt.Printf("  · %s exists — skipping (pass --force to overwrite)\n", path)
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Printf("  · wrote %s\n", path)
	return nil
}

// writeClaudeSettings merges arbiter hooks into ~/.claude/settings.json.
// A timestamped backup of any pre-existing settings is written alongside.
func writeClaudeSettings(skipMCP bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")

	var existing map[string]any
	if raw, err := os.ReadFile(settingsPath); err == nil {
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &existing); err != nil {
				return fmt.Errorf("parse %s: %w", settingsPath, err)
			}
			backup := fmt.Sprintf("%s.arbiter-backup-%s", settingsPath, time.Now().Format("20060102-150405"))
			if err := os.WriteFile(backup, raw, 0o644); err != nil {
				return err
			}
			fmt.Printf("  · backed up existing settings → %s\n", backup)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if existing == nil {
		existing = map[string]any{}
	}

	mergeArbiterHooks(existing)
	if !skipMCP {
		mergeArbiterMCP(existing)
	}

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("  · merged hooks into %s\n", settingsPath)
	return nil
}

// mergeArbiterHooks ensures the standard arbiter hook entries exist
// alongside whatever the user already has configured.
func mergeArbiterHooks(settings map[string]any) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	for _, ev := range []struct {
		event string
		cmd   string
	}{
		{"PreToolUse", "arbiter hook pretool"},
		{"PostToolUse", "arbiter hook posttool"},
		{"Notification", "arbiter hook notification"},
		{"Stop", "arbiter hook stop"},
	} {
		appendHookOnce(hooks, ev.event, ev.cmd)
	}
}

func appendHookOnce(hooks map[string]any, event, cmd string) {
	list, _ := hooks[event].([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		inner, _ := entry["hooks"].([]any)
		for _, h := range inner {
			if m, _ := h.(map[string]any); m != nil {
				if c, _ := m["command"].(string); strings.Contains(c, "arbiter hook ") {
					return // already present
				}
			}
		}
	}
	list = append(list, map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": cmd},
		},
	})
	hooks[event] = list
}

func mergeArbiterMCP(settings map[string]any) {
	mcp, _ := settings["mcpServers"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
		settings["mcpServers"] = mcp
	}
	if _, ok := mcp["godx-arbiter"]; ok {
		return
	}
	mcp["godx-arbiter"] = map[string]any{
		"command": "arbiter",
		"args":    []any{"mcp"},
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func ifelse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// rulesTemplate returns a starter rules.md body for the chosen template.
func rulesTemplate(template string) string {
	switch template {
	case "strict":
		return strictRulesTemplate
	case "sandbox":
		return sandboxRulesTemplate
	default:
		return balancedRulesTemplate
	}
}

func policyTemplate(template string) string {
	switch template {
	case "strict":
		return strictPolicyTemplate
	case "sandbox":
		return sandboxPolicyTemplate
	default:
		return balancedPolicyTemplate
	}
}

const balancedRulesTemplate = `---
agent_model: claude-haiku-4-5-20251001
timeout_seconds: 30
on_timeout: deny
on_error: approve
notify_channels: [desktop]
---

# Arbiter rules

This file is read by godx-arbiter on every tool call. It serves both as
human documentation AND as the system prompt for the slow-path agent.

## Auto-approve

- All Read, Glob, Grep tool calls (read-only by definition).
- Read-only Bash: ` + "`ls`, `cat`, `head`, `tail`, `wc`, `grep`, `rg`, `fd`, `git status`, `git log`, `git diff`, `git branch`, `git show`" + `.
- Test commands: ` + "`go test`, `npm test`, `pnpm test`, `make test`" + `.
- Edits to test files (` + "`*_test.go`, `*.test.ts`, `__tests__/*`" + `).
- Edits to documentation (` + "`*.md`" + `) outside ` + "`docs/api/`" + `.

## Deny

- ` + "`rm -rf`" + ` of any path NOT under ` + "`/tmp`, `node_modules`, `dist/`, `build/`" + `.
- ` + "`git push --force`" + ` or ` + "`--force-with-lease`" + ` to ` + "`master`, `main`, `production`" + `.
- Edits to ` + "`*.env*`, `*.pem`, `*.key`, `*credentials*`, `*token*`" + `.
- ` + "`curl ... | sh`" + ` or any pipe-to-shell pattern.

## Escalate

- Database migrations.
- Bash with ` + "`sudo`" + `.
- New dependencies (` + "`go.mod`, `package.json`" + ` etc).
- Operations affecting more than 10 files in a single tool call.
- Anything under ` + "`infrastructure/`, `terraform/`, `k8s/`" + `.

## Custom

- Files whose first 5 lines contain ` + "`// CRITICAL`, `// FROZEN`, or `// DO NOT EDIT`" + `:
  deny outright, do not even escalate.

## Diagnostics for the agent

If the agent is unsure, it should call:
- ` + "`check_rule(<keyword>)`" + ` — re-fetch a section of this file
- ` + "`read_file(<path>)`" + ` — inspect the file Claude proposes to edit
- ` + "`lookup_history(<pattern>)`" + ` — see how similar past calls were decided
- ` + "`analyze_risk(...)`" + ` — fuzzy risk classification

Only escalate after these tools have been exhausted and the answer
remains genuinely ambiguous.
`

const strictRulesTemplate = `---
agent_model: claude-sonnet-4-6
timeout_seconds: 60
on_timeout: deny
on_error: deny
notify_channels: [telegram, desktop]
---

# Arbiter rules — strict

Default-deny posture. Only explicitly safe operations approve without
human review.

## Auto-approve

- ` + "`Read`, `Glob`, `Grep`" + ` only.

## Deny

- Any write/edit/bash that isn't explicitly listed under Escalate.

## Escalate

- All ` + "`Edit`, `Write`, `Bash`" + ` operations not in the auto-approve list.
- Decisions are made by the human user, not the agent, in this template.
`

const sandboxRulesTemplate = `---
on_timeout: approve
on_error: approve
notify_channels: [desktop]
---

# Arbiter rules — sandbox

Personal sandbox project. Approve almost everything; only the
genuinely-catastrophic patterns are blocked.

## Auto-approve

- Everything except the deny list below.

## Deny

- ` + "`rm -rf /`" + ` (obviously).
- Edits outside this directory tree.
- ` + "`curl ... | sh`" + `.
`

const balancedPolicyTemplate = `# Fast-path regex rules. Optional companion to rules.md. See
# docs/POLICY_SPEC.md.
version: 1
default: agent

deny:
  - tool: Bash
    pattern: '\brm\s+-rf\s+/(etc|usr|var|opt|bin|sbin|home|root|boot|sys|proc|lib|lib64|dev)\b'
    reason: rm -rf of system-critical root directory
  - tool: Bash
    pattern: '\bgit\s+push\b.*--force(?:-with-lease)?\b.*\b(master|main|production)\b'
    reason: force push to protected branch
  - tool: Edit
    field: file_path
    pattern: '(\.env(\.|$)|\.pem$|\.key$|\.crt$|credentials|secret|\btoken\b)'
    reason: secret-bearing file
  - tool: Write
    field: file_path
    pattern: '(\.env(\.|$)|\.pem$|\.key$|\.crt$|credentials|secret|\btoken\b)'
    reason: secret-bearing file
  - tool: Bash
    pattern: 'curl[^|]*\|\s*(bash|sh|zsh)'
    reason: piping curl into shell
  - tool: Bash
    pattern: 'wget[^|]*\|\s*(bash|sh|zsh)'
    reason: piping wget into shell

allow:
  - tool: Read
  - tool: Glob
  - tool: Grep
  - tool: Bash
    pattern: '^(ls|cat|head|tail|wc|stat|file|which|whereis|type|pwd|env|date|hostname|whoami|id)\b'
    reason: read-only system command
  - tool: Bash
    pattern: '^(grep|rg|fd|find|locate|ack|ag)\b'
    reason: read-only search command
  - tool: Bash
    pattern: '^git\s+(status|log|diff|show|branch|remote|config\s+--get|rev-parse)\b'
    reason: read-only git command
  - tool: Bash
    pattern: '^(go|npm|pnpm|yarn|cargo|make)\s+(test|vet|fmt|lint|build|check|version|--version|--help)\b'
    reason: read-only or test command

to_agent:
  - tool: Bash
    pattern: '\bsudo\b'
    reason: sudo always escalates via agent
`

const strictPolicyTemplate = `version: 1
default: agent

allow:
  - tool: Read
  - tool: Glob
  - tool: Grep

# Most decisions go through the slow-path agent under strict mode.
`

const sandboxPolicyTemplate = `version: 1
default: approve

deny:
  - tool: Bash
    pattern: '\brm\s+-rf\s+/(etc|usr|var|opt|bin|sbin|home|root|boot|sys|proc|lib|lib64|dev)\b'
    reason: rm -rf of system path
  - tool: Bash
    pattern: 'curl[^|]*\|\s*(bash|sh|zsh)'
    reason: piping curl into shell
`
