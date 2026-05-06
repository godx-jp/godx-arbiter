package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DelegateTo runs another agentic CLI non-interactively for a sub-task
// and returns its output to the caller. Per docs/MULTI_CLI.md "Pattern
// 1 — delegate_to MCP tool".
//
// Each supported CLI has a small adapter (cliCommand) that knows the
// flags for headless / batch mode. Unknown CLIs error out with a hint.
type DelegateTo struct {
	// runner is injected so tests can stub exec.
	runner func(ctx context.Context, name string, args []string, stdin string) ([]byte, error)
}

// NewDelegateTo constructs the tool with the default exec runner.
func NewDelegateTo() *DelegateTo {
	return &DelegateTo{runner: defaultDelegateRunner}
}

// Name implements Tool.
func (DelegateTo) Name() string { return "delegate_to" }

// Description implements Tool.
func (DelegateTo) Description() string {
	return "Run another agentic CLI (claude, codex, gemini, antigravity) non-interactively for a sub-task. Returns the CLI's final output."
}

// InputSchema implements Tool.
func (DelegateTo) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cli":             map[string]any{"type": "string", "enum": []string{"claude", "codex", "gemini", "antigravity"}},
			"task":            map[string]any{"type": "string"},
			"context":         map[string]any{"type": "object", "description": "Free-form context handed to the delegate"},
			"budget_tokens":   map[string]any{"type": "integer", "default": 20000},
			"timeout_seconds": map[string]any{"type": "integer", "default": 120},
		},
		"required": []string{"cli", "task"},
	}
}

type delegateInput struct {
	CLI            string         `json:"cli"`
	Task           string         `json:"task"`
	Context        map[string]any `json:"context"`
	BudgetTokens   int            `json:"budget_tokens"`
	TimeoutSeconds int            `json:"timeout_seconds"`
}

type delegateOutput struct {
	CLI        string `json:"cli"`
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// Execute implements Tool.
func (d *DelegateTo) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in delegateInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.CLI == "" || in.Task == "" {
		return nil, errors.New("cli and task are required")
	}
	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bin, args, stdin, err := cliCommand(in)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	out, err := d.runner(ctx, bin, args, stdin)
	if err != nil {
		return nil, err
	}

	const maxBytes = 16 * 1024
	truncated := false
	if len(out) > maxBytes {
		out = out[:maxBytes]
		truncated = true
	}
	return json.Marshal(delegateOutput{
		CLI:        in.CLI,
		Output:     string(out),
		Truncated:  truncated,
		DurationMs: time.Since(start).Milliseconds(),
	})
}

// cliCommand returns (bin, args, stdin) for the requested CLI.
func cliCommand(in delegateInput) (string, []string, string, error) {
	prompt := buildDelegationPrompt(in)
	switch in.CLI {
	case "claude":
		// Claude Code's headless flag prints final assistant text.
		return "claude", []string{"--print"}, prompt, nil
	case "codex":
		return "codex", []string{"--print"}, prompt, nil
	case "gemini":
		return "gemini", []string{"-p"}, prompt, nil
	case "antigravity":
		return "antigravity", []string{"run"}, prompt, nil
	}
	return "", nil, "", fmt.Errorf("unsupported CLI: %s", in.CLI)
}

func buildDelegationPrompt(in delegateInput) string {
	var b strings.Builder
	b.WriteString("godx-arbiter delegated task — operate autonomously, return your final result as the last message.\n\n")
	if len(in.Context) > 0 {
		raw, _ := json.MarshalIndent(in.Context, "", "  ")
		b.WriteString("Context:\n")
		b.WriteString(string(raw))
		b.WriteString("\n\n")
	}
	if in.BudgetTokens > 0 {
		fmt.Fprintf(&b, "Token budget: %d. Be concise.\n\n", in.BudgetTokens)
	}
	b.WriteString("Task:\n")
	b.WriteString(in.Task)
	b.WriteString("\n")
	return b.String()
}

func defaultDelegateRunner(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.CombinedOutput()
}
