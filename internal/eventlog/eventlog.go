// Package eventlog appends structured decision records to a JSONL file
// at ~/.local/share/godx-arbiter/events.jsonl.
//
// The log feeds two consumers:
//   - The lookup_history tool — searches recent events to find similar
//     past decisions for consistency.
//   - `arbiter explain` — replays a past decision with full rationale.
//
// The schema is intentionally append-only and forward-compatible:
// readers ignore unknown fields, writers add them freely.
package eventlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Event is one record in the log.
type Event struct {
	TS         time.Time      `json:"ts"`
	EventID    string         `json:"event_id"`
	SessionID  string         `json:"session_id,omitempty"`
	Project    string         `json:"project,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	InputSum   string         `json:"input_summary,omitempty"`
	Path       string         `json:"path,omitempty"` // fast-path | slow-path | escalation | kill-switch | run
	Decision   string         `json:"decision"`
	Reason     string         `json:"reason,omitempty"`
	RulesSHA   string         `json:"rules_sha,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	Agent      *AgentTrace    `json:"agent,omitempty"`
	Run        *RunInfo       `json:"run,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// RunInfo summarizes an `arbiter run` invocation. Populated only when
// the eventlog row was emitted by the runner (Path == "run"). Lets
// `arbiter explain --last -v` show run-specific metadata without
// reading the per-run JSONL log.
type RunInfo struct {
	CLI       string `json:"cli"`
	Model     string `json:"model,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Outcome   string `json:"outcome"`
	LogPath   string `json:"log_path,omitempty"`
	InputTok  int    `json:"input_tokens,omitempty"`
	OutputTok int    `json:"output_tokens,omitempty"`
	Turns     int    `json:"turns,omitempty"`
}

// AgentTrace captures the slow-path agent's reasoning for replay.
type AgentTrace struct {
	Model     string       `json:"model,omitempty"`
	Iters     int          `json:"iters,omitempty"`
	ToolCalls []AgentTool  `json:"tool_calls,omitempty"`
	Final     string       `json:"final,omitempty"`
	Tokens    *TokenCounts `json:"tokens,omitempty"`
}

// AgentTool is a single tool invocation by the agent.
type AgentTool struct {
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input,omitempty"`
	Output string          `json:"output,omitempty"`
	Err    string          `json:"err,omitempty"`
}

// TokenCounts summarizes per-decision token usage.
type TokenCounts struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// DefaultPath returns the canonical eventlog path.
//
// Honors $XDG_DATA_HOME and $GODX_ARBITER_HOME (the latter wins for
// tests + sandbox setups).
func DefaultPath() string {
	if v := os.Getenv("GODX_ARBITER_LOG_PATH"); v != "" {
		return v
	}
	if v := os.Getenv("GODX_ARBITER_HOME"); v != "" {
		return filepath.Join(v, "events.jsonl")
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "godx-arbiter", "events.jsonl")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "godx-arbiter", "events.jsonl")
}

var writeMu sync.Mutex

// Append writes one event to the default eventlog. Cheap, sync,
// best-effort: returns errors but callers in the hook hot-path treat
// them as warnings (we never break the calling session — ADR-005).
func Append(ev Event) error {
	return AppendTo(DefaultPath(), ev)
}

// AppendTo writes one event to a specific file (used by tests).
func AppendTo(path string, ev Event) error {
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	if ev.EventID == "" {
		ev.EventID = newID(ev.TS)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(ev)
}

// LookupOpts filters Lookup results.
type LookupOpts struct {
	Tool      string
	Pattern   string // substring or regex (matched as substring for now)
	SessionID string
	Limit     int
	Path      string // override eventlog path; zero-value uses DefaultPath
}

// Lookup scans the eventlog and returns the most recent matches first.
//
// The eventlog can grow large; this scans the file once and keeps a
// rolling top-N. Good enough up to ~100k events; revisit if anyone
// reports a slow `lookup_history`.
func Lookup(opts LookupOpts) ([]Event, error) {
	path := opts.Path
	if path == "" {
		path = DefaultPath()
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var matches []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if !lookupMatch(ev, opts) {
			continue
		}
		matches = append(matches, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Most recent first.
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func lookupMatch(ev Event, opts LookupOpts) bool {
	if opts.Tool != "" && ev.Tool != opts.Tool {
		return false
	}
	if opts.SessionID != "" && ev.SessionID != opts.SessionID {
		return false
	}
	if opts.Pattern != "" && !strings.Contains(ev.InputSum, opts.Pattern) {
		return false
	}
	return true
}

// ExplainOpts drives `arbiter explain`.
type ExplainOpts struct {
	Last      bool
	SessionID string
	EventID   string
	Verbose   bool
	Path      string
}

// Explain renders a single event (or the most recent one) for the CLI.
func Explain(opts ExplainOpts) (string, error) {
	path := opts.Path
	if path == "" {
		path = DefaultPath()
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "(no decisions logged yet — eventlog is empty)\n", nil
		}
		return "", err
	}
	defer f.Close()

	var pick Event
	var found bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch {
		case opts.EventID != "" && ev.EventID == opts.EventID:
			pick, found = ev, true
		case opts.SessionID != "" && ev.SessionID == opts.SessionID:
			pick, found = ev, true
		case opts.Last:
			pick, found = ev, true
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no matching event found")
	}
	return formatExplain(pick, opts.Verbose), nil
}

func formatExplain(ev Event, verbose bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Event %s\n", ev.EventID)
	fmt.Fprintf(&b, "  ts:        %s\n", ev.TS.Format(time.RFC3339))
	if ev.SessionID != "" {
		fmt.Fprintf(&b, "  session:   %s\n", ev.SessionID)
	}
	if ev.Project != "" {
		fmt.Fprintf(&b, "  project:   %s\n", ev.Project)
	}
	if ev.Tool != "" {
		fmt.Fprintf(&b, "  tool:      %s\n", ev.Tool)
	}
	if ev.InputSum != "" {
		fmt.Fprintf(&b, "  input:     %s\n", ev.InputSum)
	}
	fmt.Fprintf(&b, "  path:      %s\n", ev.Path)
	fmt.Fprintf(&b, "  decision:  %s\n", ev.Decision)
	if ev.Reason != "" {
		fmt.Fprintf(&b, "  reason:    %s\n", ev.Reason)
	}
	if ev.RulesSHA != "" {
		fmt.Fprintf(&b, "  rules SHA: %s\n", ev.RulesSHA)
	}
	if ev.DurationMs > 0 {
		fmt.Fprintf(&b, "  duration:  %dms\n", ev.DurationMs)
	}
	if ev.Agent != nil {
		fmt.Fprintln(&b, "  agent:")
		fmt.Fprintf(&b, "    model: %s\n", ev.Agent.Model)
		fmt.Fprintf(&b, "    iters: %d\n", ev.Agent.Iters)
		if ev.Agent.Tokens != nil {
			fmt.Fprintf(&b, "    tokens: in=%d out=%d\n", ev.Agent.Tokens.Input, ev.Agent.Tokens.Output)
		}
		if verbose {
			for i, tc := range ev.Agent.ToolCalls {
				fmt.Fprintf(&b, "    tool[%d]: %s\n", i, tc.Name)
				if len(tc.Input) > 0 {
					fmt.Fprintf(&b, "      input: %s\n", string(tc.Input))
				}
				if tc.Output != "" {
					fmt.Fprintf(&b, "      output: %s\n", truncate(tc.Output, 240))
				}
				if tc.Err != "" {
					fmt.Fprintf(&b, "      err: %s\n", tc.Err)
				}
			}
			if ev.Agent.Final != "" {
				fmt.Fprintf(&b, "    final: %s\n", ev.Agent.Final)
			}
		}
	}
	if ev.Run != nil {
		fmt.Fprintln(&b, "  run:")
		if ev.Run.CLI != "" {
			fmt.Fprintf(&b, "    cli:      %s\n", ev.Run.CLI)
		}
		if ev.Run.Model != "" {
			fmt.Fprintf(&b, "    model:    %s\n", ev.Run.Model)
		}
		fmt.Fprintf(&b, "    outcome:  %s (exit %d)\n", ev.Run.Outcome, ev.Run.ExitCode)
		if ev.Run.InputTok > 0 || ev.Run.OutputTok > 0 {
			fmt.Fprintf(&b, "    tokens:   in=%d out=%d\n", ev.Run.InputTok, ev.Run.OutputTok)
		}
		if ev.Run.Turns > 0 {
			fmt.Fprintf(&b, "    turns:    %d\n", ev.Run.Turns)
		}
		if ev.Run.LogPath != "" {
			fmt.Fprintf(&b, "    log:      %s\n", ev.Run.LogPath)
		}
		if verbose && ev.Run.LogPath != "" {
			fmt.Fprintln(&b, "    (re-run with `jq` over the log to see the full transcript)")
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// newID derives a short event ID — ts in nano + 4 random hex bytes is
// plenty unique for a single-host append-only log.
func newID(ts time.Time) string {
	var buf [4]byte
	if _, err := io.ReadFull(randReader(), buf[:]); err != nil {
		return fmt.Sprintf("%d", ts.UnixNano())
	}
	return fmt.Sprintf("%x-%x", ts.UnixNano(), buf)
}
