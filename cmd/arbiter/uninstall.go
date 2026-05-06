package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runUninstall removes arbiter's hook + MCP entries from
// ~/.claude/settings.json. The .arbiter/ directory in the project tree
// is left alone (per docs/INSTALL.md uninstall guidance) — users who
// want to wipe project rules should rm -rf it themselves.
//
// Always writes a timestamped backup before mutating settings.json.
func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	_ = fs.Parse(args)

	home, err := os.UserHomeDir()
	if err != nil {
		fail("uninstall: home dir: %v", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no ~/.claude/settings.json — nothing to remove")
			return
		}
		fail("uninstall: read %s: %v", settingsPath, err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		fail("uninstall: parse settings.json: %v", err)
	}

	hooksRemoved := stripArbiterHooks(settings)
	mcpRemoved := stripArbiterMCP(settings)
	if !hooksRemoved && !mcpRemoved {
		fmt.Println("no arbiter entries found in settings.json")
		return
	}

	if *dryRun {
		fmt.Printf("dry-run: would remove (hooks=%v, mcp=%v) from %s\n", hooksRemoved, mcpRemoved, settingsPath)
		return
	}

	backup := fmt.Sprintf("%s.arbiter-backup-%s", settingsPath, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, raw, 0o644); err != nil {
		fail("uninstall: backup: %v", err)
	}
	fmt.Printf("  · backed up settings → %s\n", backup)

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		fail("uninstall: marshal: %v", err)
	}
	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		fail("uninstall: write: %v", err)
	}
	fmt.Println("  · arbiter hooks + MCP removed from settings.json")
	fmt.Println("    (project .arbiter/ directories are untouched)")
}

func stripArbiterHooks(settings map[string]any) bool {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false
	}
	removed := false
	for event, raw := range hooks {
		list, _ := raw.([]any)
		filtered := make([]any, 0, len(list))
		for _, item := range list {
			entry, _ := item.(map[string]any)
			inner, _ := entry["hooks"].([]any)
			isArbiter := false
			for _, h := range inner {
				if m, _ := h.(map[string]any); m != nil {
					if c, _ := m["command"].(string); strings.Contains(c, "arbiter hook ") {
						isArbiter = true
					}
				}
			}
			if isArbiter {
				removed = true
				continue
			}
			filtered = append(filtered, item)
		}
		if len(filtered) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = filtered
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return removed
}

func stripArbiterMCP(settings map[string]any) bool {
	mcp, ok := settings["mcpServers"].(map[string]any)
	if !ok {
		return false
	}
	if _, ok := mcp["godx-arbiter"]; ok {
		delete(mcp, "godx-arbiter")
		if len(mcp) == 0 {
			delete(settings, "mcpServers")
		}
		return true
	}
	return false
}
