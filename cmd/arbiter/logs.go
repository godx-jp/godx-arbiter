package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/godx-team/godx-arbiter/internal/eventlog"
)

// runLogs tails or filters the decision eventlog.
//
//	arbiter logs                          # last 20 entries
//	arbiter logs --tail                   # follow new entries
//	arbiter logs --session <id>
//	arbiter logs --tool Bash
//	arbiter logs --decision deny
//	arbiter logs --since 2026-05-01T00:00:00Z
//	arbiter logs --json                   # raw JSONL passthrough
func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	tail := fs.Bool("tail", false, "follow the eventlog and stream new entries")
	limit := fs.Int("n", 20, "show the last N entries (when not tailing)")
	session := fs.String("session", "", "filter by session id")
	tool := fs.String("tool", "", "filter by tool name")
	decision := fs.String("decision", "", "filter by decision (allow|deny)")
	since := fs.String("since", "", "RFC3339 lower bound on event timestamp")
	asJSON := fs.Bool("json", false, "print raw JSONL passthrough instead of the human format")
	_ = fs.Parse(args)

	path := eventlog.DefaultPath()
	filt := logFilter{
		session: *session, tool: *tool, decision: *decision,
	}
	if *since != "" {
		t, err := time.Parse(time.RFC3339, *since)
		if err != nil {
			fail("logs: invalid --since: %v", err)
		}
		filt.since = t
	}

	if *tail {
		if err := tailLogs(path, filt, *asJSON); err != nil {
			fail("logs: %v", err)
		}
		return
	}
	if err := scanLogs(path, filt, *limit, *asJSON); err != nil {
		fail("logs: %v", err)
	}
}

type logFilter struct {
	session, tool, decision string
	since                   time.Time
}

func (f logFilter) match(ev eventlog.Event) bool {
	if f.session != "" && ev.SessionID != f.session {
		return false
	}
	if f.tool != "" && ev.Tool != f.tool {
		return false
	}
	if f.decision != "" && !strings.EqualFold(ev.Decision, f.decision) {
		return false
	}
	if !f.since.IsZero() && ev.TS.Before(f.since) {
		return false
	}
	return true
}

func scanLogs(path string, filt logFilter, limit int, asJSON bool) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("(no decisions logged yet)")
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Two-pass: collect, then print last N matches. Eventlog files
	// are bounded by retention in practice so this is fine.
	var matches []eventlog.Event
	var rawLines []string
	for sc.Scan() {
		line := sc.Text()
		var ev eventlog.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if !filt.match(ev) {
			continue
		}
		matches = append(matches, ev)
		rawLines = append(rawLines, line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[len(matches)-limit:]
		rawLines = rawLines[len(rawLines)-limit:]
	}
	for i, ev := range matches {
		if asJSON {
			fmt.Println(rawLines[i])
		} else {
			fmt.Println(formatEventLine(ev))
		}
	}
	return nil
}

// tailLogs blocks, polling the eventlog for new entries.
func tailLogs(path string, filt logFilter, asJSON bool) error {
	if err := os.MkdirAll(parentDir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			return err
		}
		var ev eventlog.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if !filt.match(ev) {
			continue
		}
		if asJSON {
			fmt.Print(line)
		} else {
			fmt.Println(formatEventLine(ev))
		}
	}
}

func parentDir(path string) string {
	if i := strings.LastIndex(path, string(os.PathSeparator)); i >= 0 {
		return path[:i]
	}
	return "."
}

func formatEventLine(ev eventlog.Event) string {
	ts := ev.TS.Format("2006-01-02 15:04:05")
	tool := ev.Tool
	if tool == "" {
		tool = "—"
	}
	input := ev.InputSum
	if len(input) > 60 {
		input = input[:60] + "…"
	}
	return fmt.Sprintf("%s  %-8s  %-10s  %s  %s",
		ts, ev.Decision, tool, ev.SessionID, input)
}
