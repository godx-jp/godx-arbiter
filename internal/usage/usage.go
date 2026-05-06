// Package usage records token + cost information per provider call and
// summarizes it on demand for the `arbiter usage` command.
//
// The ledger format is JSON-lines for the same reason the eventlog uses
// it: append-only, easy to tail, easy to grep, easy to evolve.
package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is a single billing entry.
type Record struct {
	TS           time.Time `json:"ts"`
	SessionID    string    `json:"session,omitempty"`
	CLI          string    `json:"cli,omitempty"`
	Provider     string    `json:"provider,omitempty"` // anthropic|openai|google
	Model        string    `json:"model,omitempty"`
	InputTokens  int       `json:"input_tokens,omitempty"`
	OutputTokens int       `json:"output_tokens,omitempty"`
	CostUSD      float64   `json:"cost_usd,omitempty"`
	Path         string    `json:"path,omitempty"` // proxy|agent|escalation
	Project      string    `json:"project,omitempty"`
}

// DefaultPath returns the canonical usage.jsonl location.
func DefaultPath() string {
	if v := os.Getenv("GODX_ARBITER_USAGE_PATH"); v != "" {
		return v
	}
	if v := os.Getenv("GODX_ARBITER_HOME"); v != "" {
		return filepath.Join(v, "usage.jsonl")
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "godx-arbiter", "usage.jsonl")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "godx-arbiter", "usage.jsonl")
}

var writeMu sync.Mutex

// Append writes a single record to the default ledger.
func Append(r Record) error { return AppendTo(DefaultPath(), r) }

// AppendTo writes to a specific path (testing).
func AppendTo(path string, r Record) error {
	if r.TS.IsZero() {
		r.TS = time.Now().UTC()
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
	return enc.Encode(r)
}

// ReportOpts shapes a Report() call.
type ReportOpts struct {
	Path  string
	Today bool
	Since time.Time
}

// Report builds a human-readable summary.
func Report(opts ReportOpts) (string, error) {
	path := opts.Path
	if path == "" {
		path = DefaultPath()
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "(no usage data yet)\n", nil
		}
		return "", err
	}
	defer f.Close()

	var since time.Time
	switch {
	case !opts.Since.IsZero():
		since = opts.Since
	case opts.Today:
		now := time.Now()
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}

	type aggKey struct{ session, cli, model string }
	type agg struct {
		input, output int
		cost          float64
	}
	bucket := map[aggKey]*agg{}
	var totalIn, totalOut int
	var totalCost float64

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if !since.IsZero() && r.TS.Before(since) {
			continue
		}
		k := aggKey{r.SessionID, r.CLI, r.Model}
		a := bucket[k]
		if a == nil {
			a = &agg{}
			bucket[k] = a
		}
		a.input += r.InputTokens
		a.output += r.OutputTokens
		a.cost += r.CostUSD
		totalIn += r.InputTokens
		totalOut += r.OutputTokens
		totalCost += r.CostUSD
	}
	if err := sc.Err(); err != nil {
		return "", err
	}

	keys := make([]aggKey, 0, len(bucket))
	for k := range bucket {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].session != keys[j].session {
			return keys[i].session < keys[j].session
		}
		if keys[i].cli != keys[j].cli {
			return keys[i].cli < keys[j].cli
		}
		return keys[i].model < keys[j].model
	})

	var b strings.Builder
	if len(keys) == 0 {
		fmt.Fprintln(&b, "(no usage matching filters)")
		return b.String(), nil
	}
	for _, k := range keys {
		a := bucket[k]
		fmt.Fprintf(&b, "session %-12s — %-12s — %-30s — in=%d out=%d — $%.4f\n",
			abbrev(k.session, 12), nonempty(k.cli, "?"), nonempty(k.model, "?"),
			a.input, a.output, a.cost)
	}
	fmt.Fprintln(&b, strings.Repeat("─", 80))
	fmt.Fprintf(&b, "Total: in=%d out=%d — $%.4f\n", totalIn, totalOut, totalCost)
	return b.String(), nil
}

func abbrev(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func nonempty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
