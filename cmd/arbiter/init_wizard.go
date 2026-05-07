package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// initAnswers is the structured output of the wizard. We pass it to
// the scaffolder rather than writing files inside the wizard so the
// flow stays testable.
type initAnswers struct {
	TemplateLabel  string   // balanced | strict | sandbox | custom
	ProjectName    string
	ProjectKind    string   // production | sandbox | library | tool
	Languages      []string // free-form list, lowercased
	AgentModel     string
	OnError        string
	OnTimeout      string
	NotifyChannels []string
	QuietHours     string
	DenyExtras     []string // free-text deny rules from the user
	AllowExtras    []string
	EscalateExtras []string
	WantTelegram   bool
	WantMCP        bool
	RulesBody      string
	PolicyBody     string
}

// runInitWizard prompts the user, derives a personalized rules.md +
// policy.yaml, and returns the assembled answers.
//
// Designed to be friendly: every question has a default; ENTER accepts
// it. Free-form questions hit through to the rules body verbatim, so
// the user can write project-specific guardrails on day one.
func runInitWizard(root string) (initAnswers, error) {
	in := bufio.NewReader(os.Stdin)
	w := os.Stdout

	a := initAnswers{
		ProjectName:    filepath.Base(root),
		AgentModel:     "claude-haiku-4-5-20251001",
		OnError:        "approve",
		OnTimeout:      "deny",
		NotifyChannels: []string{"desktop", "log"},
		WantMCP:        true,
	}

	fmt.Fprintln(w, "godx-arbiter init wizard — let's tailor the rules to this project.")
	fmt.Fprintln(w, "Press ENTER to accept the default in [brackets].")
	fmt.Fprintln(w, "")

	a.ProjectName = promptString(in, w, "Project name", a.ProjectName)
	a.ProjectKind = promptChoice(in, w, "Project type",
		[]string{"production", "sandbox", "library", "tool"},
		"production",
		"production = strict; sandbox = lax; library/tool = balanced")

	switch a.ProjectKind {
	case "sandbox":
		a.TemplateLabel = "sandbox"
		a.OnError = "approve"
		a.OnTimeout = "approve"
	case "production":
		a.TemplateLabel = "balanced"
		a.OnError = "deny"
		a.OnTimeout = "deny"
	default:
		a.TemplateLabel = "balanced"
	}

	langInput := promptString(in, w, "Languages (comma-separated, e.g. go,ts,python)", "")
	a.Languages = splitCSV(langInput)

	a.AgentModel = promptChoice(in, w, "Slow-path agent model",
		[]string{"claude-haiku-4-5-20251001", "claude-sonnet-4-6", "claude-opus-4-7"},
		a.AgentModel,
		"haiku is cheap + fast; sonnet/opus for higher-stakes projects")

	a.OnError = promptChoice(in, w, "On internal arbiter error",
		[]string{"approve", "deny"}, a.OnError,
		"approve = fail-open (recommended); deny = paranoid")
	a.OnTimeout = promptChoice(in, w, "On agent timeout",
		[]string{"approve", "deny", "escalate"}, a.OnTimeout, "")

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "── Decisions ─────────────────────────────────────────────────────")
	fmt.Fprintln(w, "Anything in 'Deny' is a hard block — agent refuses without asking.")
	fmt.Fprintln(w, "Anything in 'Escalate' notifies you and waits for a human reply.")
	fmt.Fprintln(w, "Auto-approve runs without further analysis.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Built-in safe denies (rm -rf system paths, force-push to main, secret files,")
	fmt.Fprintln(w, "curl|sh) are always on. Add project-specific rules below.")
	fmt.Fprintln(w, "")

	a.DenyExtras = promptList(in, w, "Extra DENY rules (one per line, blank to finish)")
	a.AllowExtras = promptList(in, w, "Extra AUTO-APPROVE rules (one per line, blank to finish)")
	a.EscalateExtras = promptList(in, w, "Extra ESCALATE rules (one per line, blank to finish)")

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "── Notifications ─────────────────────────────────────────────────")
	a.WantTelegram = promptYesNo(in, w, "Use Telegram for escalations?", false)
	if a.WantTelegram {
		a.NotifyChannels = []string{"telegram", "desktop", "log"}
		fmt.Fprintln(w, "  → after init, run: arbiter auth set telegram")
		fmt.Fprintln(w, "    and: export GODX_ARBITER_TELEGRAM_CHAT_ID=<your chat id>")
	} else {
		a.NotifyChannels = []string{"desktop", "log"}
	}
	a.QuietHours = promptString(in, w, "Quiet hours (HH:MM-HH:MM, suppresses Telegram, blank = none)", "")

	fmt.Fprintln(w, "")
	a.WantMCP = promptYesNo(in, w, "Register the MCP server in ~/.claude/settings.json?", a.WantMCP)

	a.RulesBody = renderWizardRules(a)
	a.PolicyBody = renderWizardPolicy(a)
	return a, nil
}

// promptString prompts for a free-form string with a default.
func promptString(in *bufio.Reader, w io.Writer, label, def string) string {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// promptChoice asks one of a fixed set of options.
func promptChoice(in *bufio.Reader, w io.Writer, label string, options []string, def, hint string) string {
	for {
		fmt.Fprintf(w, "%s (%s) [%s]", label, strings.Join(options, "|"), def)
		if hint != "" {
			fmt.Fprintf(w, "\n  hint: %s", hint)
		}
		fmt.Fprint(w, "\n> ")
		line, err := in.ReadString('\n')
		if err != nil {
			return def
		}
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			return def
		}
		for _, o := range options {
			if line == o {
				return line
			}
		}
		fmt.Fprintf(w, "  invalid: %q. choose one of %v\n", line, options)
	}
}

// promptYesNo asks a y/n question.
func promptYesNo(in *bufio.Reader, w io.Writer, label string, def bool) bool {
	defStr := "y"
	if !def {
		defStr = "n"
	}
	for {
		fmt.Fprintf(w, "%s (y/n) [%s]: ", label, defStr)
		line, err := in.ReadString('\n')
		if err != nil {
			return def
		}
		line = strings.TrimSpace(strings.ToLower(line))
		switch line {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Fprintf(w, "  please answer y or n\n")
	}
}

// promptList collects free-form lines until a blank line is entered.
func promptList(in *bufio.Reader, w io.Writer, label string) []string {
	fmt.Fprintf(w, "%s\n", label)
	var out []string
	for {
		fmt.Fprint(w, "  - ")
		line, err := in.ReadString('\n')
		if err != nil {
			return out
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return out
		}
		out = append(out, line)
	}
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isTerminal reports whether f is a TTY. The wizard auto-falls back
// to template mode when stdin is piped in (e.g. CI, scripts).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// renderWizardRules turns wizard answers into a personalized rules.md.
//
// We blend the canned template's safety net (built-in denies stay
// enforced via policy.yaml) with the user's project-specific rules
// expressed in plain English. The agent reads both verbatim.
func renderWizardRules(a initAnswers) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "agent_model: %s\n", a.AgentModel)
	fmt.Fprintf(&b, "timeout_seconds: 30\n")
	fmt.Fprintf(&b, "on_timeout: %s\n", a.OnTimeout)
	fmt.Fprintf(&b, "on_error: %s\n", a.OnError)
	fmt.Fprintf(&b, "notify_channels: [%s]\n", strings.Join(a.NotifyChannels, ", "))
	if a.QuietHours != "" {
		fmt.Fprintf(&b, "quiet_hours: %q\n", a.QuietHours)
	}
	fmt.Fprintf(&b, "---\n\n")

	fmt.Fprintf(&b, "# Arbiter rules — %s\n\n", a.ProjectName)
	fmt.Fprintf(&b, "Project type: **%s**.", a.ProjectKind)
	if len(a.Languages) > 0 {
		fmt.Fprintf(&b, " Languages: %s.", strings.Join(a.Languages, ", "))
	}
	fmt.Fprintf(&b, "\n\n")
	fmt.Fprintf(&b, "This file is read by godx-arbiter on every tool call. It serves both as\n")
	fmt.Fprintf(&b, "human documentation AND as the system prompt for the slow-path agent.\n\n")

	fmt.Fprintf(&b, "## Auto-approve\n\n")
	fmt.Fprintf(&b, "- All Read, Glob, Grep tool calls (read-only by definition).\n")
	fmt.Fprintf(&b, "- Read-only Bash: `ls`, `cat`, `head`, `tail`, `wc`, `grep`, `rg`, `fd`, `git status`, `git log`, `git diff`, `git branch`, `git show`.\n")
	fmt.Fprintf(&b, "- Test commands: `go test`, `npm test`, `pnpm test`, `make test`.\n")
	fmt.Fprintf(&b, "- Edits to test files (`*_test.go`, `*.test.ts`, `__tests__/*`).\n")
	fmt.Fprintf(&b, "- Edits to documentation (`*.md`) outside `docs/api/`.\n")
	for _, line := range a.AllowExtras {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Deny\n\n")
	fmt.Fprintf(&b, "- `rm -rf` of any path NOT under `/tmp`, `node_modules`, `dist/`, `build/`.\n")
	fmt.Fprintf(&b, "- `git push --force` or `--force-with-lease` to `master`, `main`, `production`.\n")
	fmt.Fprintf(&b, "- Edits to `*.env*`, `*.pem`, `*.key`, `*credentials*`, `*token*`.\n")
	fmt.Fprintf(&b, "- `curl ... | sh` or any pipe-to-shell pattern.\n")
	for _, line := range a.DenyExtras {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Escalate\n\n")
	fmt.Fprintf(&b, "- Database migrations.\n")
	fmt.Fprintf(&b, "- Bash with `sudo`.\n")
	fmt.Fprintf(&b, "- New dependencies (`go.mod`, `package.json`, etc).\n")
	fmt.Fprintf(&b, "- Operations affecting more than 10 files in a single tool call.\n")
	fmt.Fprintf(&b, "- Anything under `infrastructure/`, `terraform/`, `k8s/`.\n")
	for _, line := range a.EscalateExtras {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Custom\n\n")
	fmt.Fprintf(&b, "- Files whose first 5 lines contain `// CRITICAL`, `// FROZEN`, or `// DO NOT EDIT`:\n")
	fmt.Fprintf(&b, "  deny outright, do not even escalate.\n\n")

	fmt.Fprintf(&b, "## Diagnostics for the agent\n\n")
	fmt.Fprintf(&b, "If the agent is unsure, it should call:\n")
	fmt.Fprintf(&b, "- `check_rule(<keyword>)` — re-fetch a section of this file\n")
	fmt.Fprintf(&b, "- `read_file(<path>)` — inspect the file Claude proposes to edit\n")
	fmt.Fprintf(&b, "- `lookup_history(<pattern>)` — see how similar past calls were decided\n")
	fmt.Fprintf(&b, "- `analyze_risk(...)` — fuzzy risk classification\n\n")
	fmt.Fprintf(&b, "Only escalate after these tools have been exhausted and the answer\n")
	fmt.Fprintf(&b, "remains genuinely ambiguous.\n")
	return b.String()
}

func renderWizardPolicy(a initAnswers) string {
	// Wizard always starts from the balanced policy as the safety net;
	// the personalization happens in rules.md (English) where the agent
	// can apply judgment. Putting wizard-derived rules into policy.yaml
	// would force exact regex authoring, which the wizard intentionally
	// avoids.
	body := policyTemplate(a.TemplateLabel)
	if a.ProjectKind == "production" && a.OnError == "deny" {
		body = strings.Replace(body, "default: agent", "default: agent  # production: deny on error", 1)
	}
	return body
}
