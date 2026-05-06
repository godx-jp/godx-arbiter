package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/godx-team/godx-arbiter/internal/usage"
)

// runUsage renders the per-session / per-day token + cost summary.
func runUsage(args []string) {
	fs := flag.NewFlagSet("usage", flag.ExitOnError)
	today := fs.Bool("today", false, "show today only")
	since := fs.String("since", "", "RFC3339 timestamp lower bound (e.g. 2026-05-01T00:00:00Z)")
	_ = fs.Parse(args)

	rep, err := usage.Report(usage.ReportOpts{
		Today: *today,
		Since: parseSince(*since),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[arbiter] usage: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(rep)
}

func parseSince(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[arbiter] usage: invalid --since: %v\n", err)
		os.Exit(2)
	}
	return t
}
