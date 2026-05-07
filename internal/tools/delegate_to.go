package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godx-team/godx-arbiter/internal/runner"
)

// DelegateTo runs another agentic CLI non-interactively for a sub-task
// and returns its output to the caller. Per docs/MULTI_CLI.md "Pattern
// 1 — delegate_to MCP tool".
//
// Implementation: this is a thin wrapper around internal/runner.
// `arbiter run` and `delegate_to` share the same per-CLI flag table
// (internal/runner/cliflags.go), so per-CLI invocation lives in one
// place.
type DelegateTo struct {
	// Run is injected so tests can stub the runner.
	Run func(ctx context.Context, spec runner.RunSpec) (*runner.RunResult, error)
}

// NewDelegateTo constructs the tool with the default runner.
func NewDelegateTo() *DelegateTo {
	r := runner.New()
	return &DelegateTo{Run: r.Run}
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

	// delegate_to's contract is a single string of "the CLI's final
	// output" — the historical behaviour was io.ReadAll on combined
	// stdout+stderr. Achieve the same by pointing the runner at a
	// buffer in OutputFinal mode and pulling FinalText out.
	var sink bytes.Buffer
	spec := runner.RunSpec{
		CLI:        runner.CLI(in.CLI),
		Task:       buildDelegationPrompt(in),
		Timeout:    timeout,
		OutputMode: runner.OutputFinal,
		Quiet:      true,
		Stdout:     &sink,
		Stderr:     &sink,
	}

	result, err := d.Run(ctx, spec)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("runner returned no result")
	}

	// Combine streamed text + raw stdout — for claude the renderer
	// fills FinalText; for non-claude CLIs the runner already wrote
	// stdout verbatim into `sink`, so we don't double-up.
	output := result.FinalText
	if output == "" {
		output = sink.String()
	}

	const maxBytes = 16 * 1024
	truncated := false
	if len(output) > maxBytes {
		output = output[:maxBytes]
		truncated = true
	}
	return json.Marshal(delegateOutput{
		CLI:        in.CLI,
		ExitCode:   result.ExitCode,
		Output:     output,
		Truncated:  truncated,
		DurationMs: result.DurationMs,
	})
}

// buildDelegationPrompt mirrors the formatting delegate_to has shipped
// since step 14 of the ROADMAP. Kept here (not in runner.cliflags)
// because it includes delegate-specific framing — context block,
// budget hint — that arbiter run wouldn't add.
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
