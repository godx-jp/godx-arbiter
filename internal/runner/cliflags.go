package runner

import (
	"fmt"
	"strings"
)

// CLIInvocation is the resolved argv + stdin payload for a child CLI
// spawn. The runner builds one of these from a RunSpec, then exec's
// it under a process group.
type CLIInvocation struct {
	Bin   string   // resolved later via exec.LookPath
	Args  []string // includes the headless flag (--print, etc.)
	Stdin string   // task prompt, fed via os.Pipe
}

// BuildInvocation translates a RunSpec into the per-CLI argv. Claude
// Code gets the full passthrough surface; codex/gemini/antigravity
// get the conservative single-prompt headless invocation that
// delegate_to has used since Step 14 of the ROADMAP.
//
// The single source of truth deliberately lives outside
// internal/tools/delegate_to.go — both delegate_to and the new run
// subcommand resolve the same way, so future Claude Code flag
// additions (e.g. --add-mcp-config) only need to be added once.
func BuildInvocation(spec RunSpec) (CLIInvocation, error) {
	prompt := buildPrompt(spec)
	switch spec.CLI {
	case CLIClaude, "":
		return claudeInvocation(spec, prompt), nil
	case CLICodex:
		return CLIInvocation{Bin: "codex", Args: []string{"--print"}, Stdin: prompt}, nil
	case CLIGemini:
		return CLIInvocation{Bin: "gemini", Args: []string{"-p"}, Stdin: prompt}, nil
	case CLIAntigravity:
		return CLIInvocation{Bin: "antigravity", Args: []string{"run"}, Stdin: prompt}, nil
	}
	return CLIInvocation{}, fmt.Errorf("runner: unsupported CLI %q", spec.CLI)
}

// claudeInvocation assembles the full passthrough flag set documented
// in docs/RUN.md ("Fidelity flags").
//
// Claude *always* runs with --output-format stream-json regardless of
// the user's --output mode — the runner needs the structured events
// for token accounting + tool-use rendering. The renderer (chosen by
// OutputMode) decides what to show the user; the data flow upstream
// is uniform.
func claudeInvocation(spec RunSpec, prompt string) CLIInvocation {
	args := []string{"--print", "--output-format", "stream-json", "--verbose"}
	if spec.Resume != "" {
		args = append(args, "--resume", spec.Resume)
	} else if spec.Continue {
		args = append(args, "--continue")
	}
	if len(spec.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(spec.AllowedTools, ","))
	}
	if len(spec.DeniedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(spec.DeniedTools, ","))
	}
	if spec.PermissionMode != "" {
		args = append(args, "--permission-mode", string(spec.PermissionMode))
	}
	if spec.MCPConfig != "" {
		args = append(args, "--mcp-config", spec.MCPConfig)
	}
	for _, dir := range spec.AddDirs {
		args = append(args, "--add-dir", dir)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.UnsafeSkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	return CLIInvocation{Bin: "claude", Args: args, Stdin: prompt}
}

// buildPrompt mirrors the formatting delegate_to has shipped since
// step 14 of the ROADMAP. Keeps a top-line orienting sentence so
// non-claude CLIs that don't read from settings.json still get
// arbiter context.
func buildPrompt(spec RunSpec) string {
	if spec.CLI == CLIClaude || spec.CLI == "" {
		// Claude Code reads CLAUDE.md + settings.json itself; no
		// preamble needed, the prompt is the task verbatim.
		return spec.Task
	}
	var b strings.Builder
	b.WriteString("godx-arbiter delegated task — operate autonomously, return your final result as the last message.\n\n")
	b.WriteString("Task:\n")
	b.WriteString(spec.Task)
	b.WriteString("\n")
	return b.String()
}
