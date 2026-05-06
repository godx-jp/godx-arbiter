package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/godx-team/godx-arbiter/internal/mcp"
	"github.com/godx-team/godx-arbiter/internal/tools"
)

// runMCP starts the stdio MCP server. The server reuses the same tool
// registry that the slow-path agent uses internally, so any decision-
// support tool we add lights up in both places at once (per
// docs/MCP_TOOLS.md "Adding a new tool").
func runMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := mcp.NewServer(version, tools.DefaultRegistry())
	if err := srv.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[arbiter] mcp: %v\n", err)
		os.Exit(1)
	}
}
