package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/godx-team/godx-arbiter/internal/eventlog"
)

// runExplain replays a past decision (or the most recent one) with full
// rationale: fast-path eval, agent transcript, tools called, final
// decision, rules.md SHA at the time. See docs/DECISION_FLOW.md §7.
func runExplain(args []string) {
	fs := flag.NewFlagSet("explain", flag.ExitOnError)
	last := fs.Bool("last", false, "show the most recent decision in the eventlog")
	verbose := fs.Bool("v", false, "include full agent tool transcript")
	_ = fs.Parse(args)

	rest := fs.Args()
	var sessionID, eventID string
	if len(rest) >= 1 {
		sessionID = rest[0]
	}
	if len(rest) >= 2 {
		eventID = rest[1]
	}

	if !*last && sessionID == "" {
		fmt.Fprintln(os.Stderr, "explain: usage — arbiter explain <session-id> [event-id]   |   arbiter explain --last")
		os.Exit(2)
	}

	out, err := eventlog.Explain(eventlog.ExplainOpts{
		Last:      *last,
		SessionID: sessionID,
		EventID:   eventID,
		Verbose:   *verbose,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[arbiter] explain: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out)
}
