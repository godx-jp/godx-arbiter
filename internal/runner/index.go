package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// IndexEntry is one row in runs/index.jsonl. Cwd is recorded so
// `--resume <id>` can refuse if the user accidentally points at a run
// from a different project (hooks + CLAUDE.md context would mismatch).
type IndexEntry struct {
	ID       string    `json:"id"`
	CLI      CLI       `json:"cli"`
	Model    string    `json:"model,omitempty"`
	Cwd      string    `json:"cwd"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitzero"`
	ExitCode int       `json:"exit_code"`
	Outcome  Outcome   `json:"outcome"`
	LogPath  string    `json:"log_path,omitempty"`
	Reason   string    `json:"reason,omitempty"`
}

// IndexPath is the canonical location of the runs index. Honors
// $GODX_ARBITER_HOME so tests can isolate.
func IndexPath() string {
	return filepath.Join(runsDir(), "index.jsonl")
}

// runsDir returns the directory that holds index.jsonl + per-run logs.
func runsDir() string {
	if v := os.Getenv("GODX_ARBITER_RUNS_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("GODX_ARBITER_HOME"); v != "" {
		return filepath.Join(v, "runs")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "godx-arbiter", "runs")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "godx-arbiter", "runs")
}

var indexMu sync.Mutex

// AppendIndex writes one entry to the index file. Idempotent on
// {id, ended} pairs by virtue of being append-only — the caller
// writes one row at start (Ended.IsZero) and one at end (with
// final outcome).
func AppendIndex(entry IndexEntry) error {
	path := IndexPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	indexMu.Lock()
	defer indexMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(entry)
}

// FindLatest returns the most recent end-row for a given run-id, or
// the most recent start-row if no end-row exists yet. nil + nil when
// the id has no matches.
func FindLatest(id string) (*IndexEntry, error) {
	f, err := os.Open(IndexPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var (
		latestEnd   *IndexEntry
		latestStart *IndexEntry
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e IndexEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.ID != id {
			continue
		}
		if !e.Ended.IsZero() {
			cp := e
			latestEnd = &cp
		} else {
			cp := e
			latestStart = &cp
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if latestEnd != nil {
		return latestEnd, nil
	}
	return latestStart, nil
}

// VerifyResumeCwd returns an error when --resume <id> would resume a
// session whose original cwd differs from the user's current cwd,
// unless force is true. The check exists because Claude Code reads
// CLAUDE.md + settings.json relative to cwd; swapping cwd silently
// would deliver wrong context.
func VerifyResumeCwd(id, cwd string, force bool) error {
	if id == "" {
		return nil
	}
	prev, err := FindLatest(id)
	if err != nil {
		return err
	}
	if prev == nil {
		// Unknown id (the user might be resuming a session arbiter
		// didn't initiate). Allow — claude itself will reject if the
		// session-id is bogus.
		return nil
	}
	if prev.Cwd == cwd || force {
		return nil
	}
	return fmt.Errorf("runner: refusing to resume %q from a different cwd (was %q, now %q) — pass --force-resume to override", id, prev.Cwd, cwd)
}

// ListRecent returns the latest n end-rows (or start-rows when no
// end-row exists yet, marking the run as in-flight). Most recent
// first.
func ListRecent(n int) ([]IndexEntry, error) {
	f, err := os.Open(IndexPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	// Two-pass: collect all, dedup by ID keeping the latest, sort by
	// Started desc.
	byID := map[string]IndexEntry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e IndexEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		// Prefer end-rows over start-rows; latest end-row wins.
		prev, ok := byID[e.ID]
		switch {
		case !ok:
			byID[e.ID] = e
		case prev.Ended.IsZero() && !e.Ended.IsZero():
			byID[e.ID] = e
		case e.Ended.After(prev.Ended):
			byID[e.ID] = e
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make([]IndexEntry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	// Sort by Started desc.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Started.After(out[i].Started) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// LogPathFor derives the canonical log file path for a run-id.
func LogPathFor(id string) string { return filepath.Join(runsDir(), id+".jsonl") }
