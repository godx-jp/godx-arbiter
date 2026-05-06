package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/godx-team/godx-arbiter/internal/proxy"
)

// runProxy starts the local LLM proxy. Mode B in docs/MULTI_CLI.md.
func runProxy(args []string) {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)
	addr := fs.String("addr", ":7777", "listen address (host:port)")
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := proxy.New(*addr)
	fmt.Printf("godx-arbiter proxy listening on %s\n", *addr)
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[arbiter] proxy: %v\n", err)
		os.Exit(1)
	}
}
