// Package runner spawns an agentic CLI (Claude Code, Codex, Gemini,
// Antigravity) headlessly, streams its work back to the caller in
// real time, and records the run for replay via eventlog + a
// per-run JSONL log on disk.
//
// This is the engine behind the `arbiter run` subcommand and the
// `delegate_to` MCP tool — both share the same RunSpec / RunResult
// shape so a future change to one lights up in the other.
package runner

import (
	"io"
	"time"
)

// CLI names the agentic CLI to spawn.
type CLI string

const (
	CLIClaude      CLI = "claude"
	CLICodex       CLI = "codex"
	CLIGemini      CLI = "gemini"
	CLIAntigravity CLI = "antigravity"
)

// OutputMode controls how the runner surfaces the child's output to
// the caller.
type OutputMode string

const (
	// OutputStream renders text deltas + tool calls live to stdout.
	OutputStream OutputMode = "stream"
	// OutputFinal blocks until the child exits and emits only the
	// final assistant text. Default for delegate_to / non-claude CLIs.
	OutputFinal OutputMode = "final"
	// OutputJSON passes raw stream-json through to stdout.
	OutputJSON OutputMode = "json"
)

// PermissionMode mirrors Claude Code's --permission-mode values.
type PermissionMode string

const (
	PermissionDefault     PermissionMode = "default"
	PermissionPlan        PermissionMode = "plan"
	PermissionAcceptEdits PermissionMode = "acceptEdits"
)

// RunSpec describes a single run. Zero-value fields use the defaults
// documented in cmd/arbiter/run.go.
type RunSpec struct {
	// CLI selects the child binary. Defaults to claude.
	CLI CLI

	// Task is the prompt fed to the child via stdin.
	Task string

	// Cwd is the working directory pinned for the child. Empty means
	// "use the caller's cwd" — runner.Run resolves at call time.
	Cwd string

	// Timeout is the hard wall-clock cap. Zero means "no timeout";
	// callers in production always set a non-zero value.
	Timeout time.Duration

	// OutputMode picks the renderer.
	OutputMode OutputMode

	// LogPath is the destination JSONL log. Empty means runner
	// auto-derives <runs>/<id>.jsonl.
	LogPath string

	// ID is the run identifier. Empty → auto-generated. Reused as the
	// session_id in eventlog so `arbiter explain` works.
	ID string

	// Quiet suppresses live render output but still writes the log.
	Quiet bool

	// NotifyOnDone fires notify.Default.Dispatch when the run ends.
	NotifyOnDone bool

	// --- fidelity passthrough flags (claude only) ---

	// Resume passes through to claude --resume <id>.
	Resume string

	// Continue passes through to claude --continue.
	Continue bool

	// AllowedTools / DeniedTools pass through verbatim.
	AllowedTools []string
	DeniedTools  []string

	// PermissionMode passes through to claude --permission-mode.
	PermissionMode PermissionMode

	// MCPConfig points at an extra MCP server config file (in addition
	// to the user's ~/.claude/settings.json mcpServers).
	MCPConfig string

	// AddDirs grants the child read access to additional directories
	// (claude --add-dir).
	AddDirs []string

	// Model pins the model (claude --model). Empty → claude chooses.
	Model string

	// --- safety flags ---

	// UnsafeSkipPermissions enables claude --dangerously-skip-permissions.
	// Only honored when GODX_ARBITER_ALLOW_UNSAFE=1 is set.
	UnsafeSkipPermissions bool

	// NoArbiterHooks sets GODX_ARBITER_DISABLED=1 in the child env so
	// arbiter's own hooks don't fire on the spawned session. Dev only.
	NoArbiterHooks bool

	// InheritEnv passes the caller's full environment instead of the
	// curated allowlist. Convenient for one-offs; careful with secrets.
	InheritEnv bool

	// --- IO sinks (testable) ---

	// Stdout / Stderr default to os.Stdout / os.Stderr when nil.
	Stdout io.Writer
	Stderr io.Writer
}

// RunResult is what Run returns when the child exits cleanly.
type RunResult struct {
	ID         string
	CLI        CLI
	Cwd        string
	ExitCode   int
	Outcome    Outcome
	FinalText  string
	Turns      int
	InputTok   int
	OutputTok  int
	DurationMs int64
	LogPath    string
	StartedAt  time.Time
	EndedAt    time.Time

	// Reason carries human-readable detail when Outcome != OutcomeOK.
	Reason string
}

// Outcome describes why the run ended.
type Outcome string

const (
	OutcomeOK          Outcome = "completed"
	OutcomeChildFailed Outcome = "failed"
	OutcomeTimeout     Outcome = "timeout"
	OutcomeInterrupted Outcome = "interrupted"
	OutcomeRefused     Outcome = "refused"
	OutcomeUnsafe      Outcome = "unsafe-spawn"
)

// ExitCode maps an Outcome to the process exit code documented in the
// CLI plan (0/1/2/124/130).
func (o Outcome) ExitCode() int {
	switch o {
	case OutcomeOK:
		return 0
	case OutcomeChildFailed:
		return 1
	case OutcomeRefused:
		return 2
	case OutcomeTimeout:
		return 124
	case OutcomeInterrupted:
		return 130
	default:
		// Unsafe-spawn is a successful run, just flagged.
		return 0
	}
}
