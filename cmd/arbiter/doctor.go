package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/godx-team/godx-arbiter/internal/config"
	"github.com/godx-team/godx-arbiter/internal/notify"
	"github.com/godx-team/godx-arbiter/internal/projectfind"
)

// doctorReport is the result of a single diagnostic run.
type doctorReport struct {
	OK       bool
	Sections []string
}

// runDoctor diagnoses the local arbiter installation: binary version,
// environment, project detection, parsed config, warnings.
func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	notifyTest := fs.Bool("notify-test", false, "send a test message via every available notification channel")
	_ = fs.Parse(args)

	rep := buildDoctorReport(os.Stderr)
	for _, s := range rep.Sections {
		fmt.Fprint(os.Stdout, s)
	}
	if *notifyTest {
		ok := runNotifyTest()
		if !ok {
			rep.OK = false
		}
	}
	if !rep.OK {
		os.Exit(1)
	}
}

func runNotifyTest() bool {
	fmt.Println("Notification channels (live test)")
	registry := notify.DefaultRegistry()
	all := registry.All()
	if len(all) == 0 {
		fmt.Println("  (none registered)")
		return true
	}
	ok := true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, ch := range all {
		name := ch.Name()
		if !ch.Available() {
			fmt.Printf("  %-10s ✗ unavailable (skipped)\n", name)
			continue
		}
		fmt.Printf("  %-10s … sending\n", name)
		reply, err := ch.Ask(ctx, notify.EscalateRequest{
			Question: fmt.Sprintf("godx-arbiter doctor: test notification (%s) — please ignore", name),
			Options:  []string{"approve", "deny"},
			Context:  map[string]any{"test": true, "ts": time.Now().Format(time.RFC3339)},
			Timeout:  20 * time.Second,
		})
		switch {
		case err != nil:
			fmt.Printf("  %-10s ✗ %v\n", name, err)
			ok = false
		case reply.Timeout:
			fmt.Printf("  %-10s ⚠ delivered (no reply expected for this channel)\n", name)
		default:
			fmt.Printf("  %-10s ✓ replied %q via %s\n", name, reply.Reply, reply.Channel)
		}
	}
	fmt.Println()
	return ok
}

func buildDoctorReport(stderr io.Writer) doctorReport {
	var rep doctorReport
	rep.OK = true

	rep.Sections = append(rep.Sections, sectionBinary())
	rep.Sections = append(rep.Sections, sectionEnv())

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "[arbiter] doctor: getwd: %v\n", err)
		cwd = "."
	}
	projSection, projOK := sectionProject(cwd)
	rep.Sections = append(rep.Sections, projSection)
	if !projOK {
		rep.OK = false
	}

	return rep
}

func sectionBinary() string {
	exe, _ := os.Executable()
	var b strings.Builder
	fmt.Fprintln(&b, "Binary")
	fmt.Fprintf(&b, "  version: %s\n", version)
	if exe != "" {
		fmt.Fprintf(&b, "  path:    %s\n", exe)
	}
	fmt.Fprintln(&b)
	return b.String()
}

func sectionEnv() string {
	var b strings.Builder
	fmt.Fprintln(&b, "Environment")
	fmt.Fprintf(&b, "  ANTHROPIC_API_KEY:        %s\n", presentMask("ANTHROPIC_API_KEY"))
	fmt.Fprintf(&b, "  GODX_ARBITER_HOME:        %s\n", presentValueOr("GODX_ARBITER_HOME", "(default ~/.config/godx-arbiter)"))
	fmt.Fprintf(&b, "  GODX_ARBITER_LOG_LEVEL:   %s\n", presentValueOr("GODX_ARBITER_LOG_LEVEL", "info"))
	fmt.Fprintf(&b, "  GODX_ARBITER_DISABLED:    %s\n", presentValueOr("GODX_ARBITER_DISABLED", "(not set — arbiter active)"))
	fmt.Fprintln(&b)
	return b.String()
}

// sectionProject reports project detection and config status.
// Returns (text, ok) — ok=false on hard errors so doctor exits non-zero.
func sectionProject(cwd string) (string, bool) {
	var b strings.Builder
	ok := true
	fmt.Fprintln(&b, "Project")
	fmt.Fprintf(&b, "  cwd:    %s\n", cwd)

	proj, err := config.LoadFromCwd(cwd)
	if err != nil {
		if errors.Is(err, projectfind.ErrNotFound) {
			fmt.Fprintln(&b, "  status: no .arbiter/ found in cwd or any ancestor")
			fmt.Fprintln(&b, "          (arbiter will fall back to global defaults)")
			fmt.Fprintln(&b)
			return b.String(), true
		}
		fmt.Fprintf(&b, "  status: ERROR: %v\n", err)
		fmt.Fprintln(&b)
		return b.String(), false
	}

	fmt.Fprintf(&b, "  root:   %s\n", proj.Root)

	// rules.md
	if proj.HasRules() {
		r := proj.Rules
		lines := strings.Count(r.Body, "\n") + 1
		fmt.Fprintf(&b, "  rules.md:    parsed (%d body lines)", lines)
		if !r.IsEnabled() {
			fmt.Fprint(&b, "  ⚠ enabled=false (kill switch on)")
			ok = false
		}
		fmt.Fprintln(&b)
		printRulesFrontMatter(&b, r)
	} else {
		fmt.Fprintln(&b, "  rules.md:    (absent)")
	}

	// policy.yaml
	if proj.HasPolicy() {
		p := proj.Policy
		fmt.Fprintf(&b, "  policy.yaml: parsed — %d deny, %d allow, %d to_agent (default=%s)\n",
			len(p.Deny), len(p.Allow), len(p.ToAgent), p.Default)
	} else {
		fmt.Fprintln(&b, "  policy.yaml: (absent — every call goes to slow-path)")
	}

	// Warnings
	if len(proj.Warnings) > 0 {
		fmt.Fprintln(&b, "")
		fmt.Fprintln(&b, "Warnings")
		for _, w := range proj.Warnings {
			fmt.Fprintf(&b, "  • %s\n", w)
		}
		ok = false
	}

	fmt.Fprintln(&b)
	return b.String(), ok
}

func printRulesFrontMatter(b *strings.Builder, r *config.Rules) {
	fm := r.FrontMatter
	if fm.AgentModel != "" {
		fmt.Fprintf(b, "    agent_model:                %s\n", fm.AgentModel)
	}
	if fm.TimeoutSeconds > 0 {
		fmt.Fprintf(b, "    timeout_seconds:            %d\n", fm.TimeoutSeconds)
	}
	if fm.OnTimeout != "" {
		fmt.Fprintf(b, "    on_timeout:                 %s\n", fm.OnTimeout)
	}
	if fm.OnError != "" {
		fmt.Fprintf(b, "    on_error:                   %s\n", fm.OnError)
	}
	if len(fm.NotifyChannels) > 0 {
		fmt.Fprintf(b, "    notify_channels:            %s\n", strings.Join(fm.NotifyChannels, ", "))
	}
	if fm.QuietHours != "" {
		fmt.Fprintf(b, "    quiet_hours:                %s\n", fm.QuietHours)
	}
}

func presentMask(env string) string {
	v := os.Getenv(env)
	if v == "" {
		return "✗ not set"
	}
	return fmt.Sprintf("✓ set (%d chars)", len(v))
}

func presentValueOr(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fallback
}
