package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/godx-team/godx-arbiter/internal/auth"
)

// runAuth dispatches the auth subcommand. Stores provider API keys in
// the OS keychain so subsequent invocations don't need env vars.
//
// Usage:
//
//	arbiter auth set <provider>            # prompt-style read from stdin
//	arbiter auth set <provider> <value>    # supplied on argv
//	arbiter auth get <provider>            # print to stdout if found
//	arbiter auth list                      # known providers + status
//	arbiter auth delete <provider>         # remove
//
// `provider` is one of: anthropic, openai, google, telegram.
func runAuth(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "auth: subcommand required (set|get|list|delete)")
		os.Exit(2)
	}
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "set":
		runAuthSet(rest)
	case "get":
		runAuthGet(rest)
	case "list":
		runAuthList(rest)
	case "delete", "rm":
		runAuthDelete(rest)
	default:
		fmt.Fprintf(os.Stderr, "auth: unknown subcommand %q\n", cmd)
		os.Exit(2)
	}
}

func runAuthSet(args []string) {
	fs := flag.NewFlagSet("auth set", flag.ExitOnError)
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "auth set: provider required")
		os.Exit(2)
	}
	p := auth.Provider(rest[0])
	if p.EnvVar() == "" {
		fmt.Fprintf(os.Stderr, "auth set: unknown provider %q (anthropic|openai|google|telegram)\n", rest[0])
		os.Exit(2)
	}
	var value string
	if len(rest) >= 2 {
		value = rest[1]
	} else {
		fmt.Fprintf(os.Stderr, "Enter %s credential (input echoed): ", p)
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			value = strings.TrimSpace(sc.Text())
		}
	}
	if value == "" {
		fmt.Fprintln(os.Stderr, "auth set: empty value")
		os.Exit(2)
	}
	location, err := auth.Set(p, value)
	if err != nil {
		fail("auth set: %v", err)
	}
	fmt.Printf("✓ stored %s in %s\n", p, location)
}

func runAuthGet(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "auth get: provider required")
		os.Exit(2)
	}
	p := auth.Provider(args[0])
	v, err := auth.Get(p)
	if err != nil {
		fail("auth get: %v", err)
	}
	if v == "" {
		fmt.Fprintf(os.Stderr, "no credential stored for %s\n", p)
		os.Exit(1)
	}
	fmt.Println(v)
}

func runAuthList(_ []string) {
	all := []auth.Provider{auth.ProviderAnthropic, auth.ProviderOpenAI, auth.ProviderGoogle, auth.ProviderTelegram}
	for _, p := range all {
		v, _ := auth.Get(p)
		status := "✗ not set"
		if v != "" {
			status = fmt.Sprintf("✓ %d chars", len(v))
		}
		fmt.Printf("  %-12s %s   (env: %s)\n", p, status, p.EnvVar())
	}
}

func runAuthDelete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "auth delete: provider required")
		os.Exit(2)
	}
	p := auth.Provider(args[0])
	if err := auth.Delete(p); err != nil {
		fail("auth delete: %v", err)
	}
	fmt.Printf("✓ removed %s\n", p)
}
