package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/projectfind"
	"github.com/godx-team/godx-arbiter/internal/proxy"
)

// runProxy starts the local LLM proxy. Mode B in docs/MULTI_CLI.md.
func runProxy(args []string) {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)
	addr := fs.String("addr", ":7777", "listen address (host:port)")
	cli := fs.String("cli", "claude-code", "CLI label used for routing + usage logging")
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cwd, _ := os.Getwd()
	var proj *config.Project
	if p, err := config.LoadFromCwd(cwd); err == nil {
		proj = p
	} else if !errors.Is(err, projectfind.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "[arbiter] proxy: project config: %v\n", err)
	}

	wiring := proxy.NewWiring(proj, *cli)
	srv := proxy.New(*addr).WithHooks(wiring.Hooks())

	fmt.Printf("godx-arbiter proxy listening on %s (cli=%s)\n", *addr, *cli)
	if proj != nil {
		fmt.Printf("  project: %s\n", proj.Root)
	}
	if err := srv.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "[arbiter] proxy: %v\n", err)
		os.Exit(1)
	}
}
