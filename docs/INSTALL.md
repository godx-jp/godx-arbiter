# Installation

godx-arbiter ships as a single static Go binary. Three install paths:

## 1. npm (recommended for Node-using devs)

```bash
npm install -g godx-arbiter
```

The npm package is a tiny wrapper. On `npm install`, a postinstall
script runs `npm/install.js`, which:

1. Detects platform + arch (`linux-x64`, `darwin-arm64`, `windows-x64`, ...)
2. Downloads the matching binary from the project's GitHub Releases
3. Verifies the SHA-256 checksum
4. Symlinks `arbiter` into the npm bin path (so it's on `$PATH`)

The npm package itself does **not** include the binary — it's downloaded
fresh per platform. This pattern is used by `esbuild`, `swc`,
`@biomejs/biome`, `turbo`, etc.

Update: `npm update -g godx-arbiter` re-runs the install script.

## 2. curl (no Node required)

```bash
curl -sSL https://godx-arbiter.dev/install.sh | bash
```

Same effect as npm: downloads binary, verifies checksum, installs to
`~/.local/bin/arbiter` (or `$PREFIX/bin/arbiter` if `PREFIX` is set).

## 3. Manual binary download

Grab the binary for your platform from
`https://github.com/<org>/godx-arbiter/releases/latest`, verify
checksum, place on `$PATH`.

```bash
# example for linux-amd64
curl -L https://github.com/<org>/godx-arbiter/releases/download/vX.Y.Z/arbiter-linux-amd64.tar.gz \
  | tar xz -C ~/.local/bin
```

## After install

```bash
arbiter --version       # confirm install
arbiter init            # set up hooks + project rules
arbiter doctor          # check everything is wired up
```

### `arbiter init`

Interactive setup. Performs:

1. **Adds hook config** to `~/.claude/settings.json`:

   ```json
   {
     "hooks": {
       "PreToolUse":    [{"hooks": [{"type": "command", "command": "arbiter hook pretool"}]}],
       "Notification":  [{"hooks": [{"type": "command", "command": "arbiter hook notification"}]}],
       "Stop":          [{"hooks": [{"type": "command", "command": "arbiter hook stop"}]}]
     }
   }
   ```

   (Merges with existing settings. Backup written to
   `~/.claude/settings.json.arbiter-backup-<ts>`.)

2. **Creates `.arbiter/rules.md`** in current directory using the chosen
   template (`strict` / `relaxed` / `sandbox`).

3. **Optional MCP registration** — adds `arbiter mcp` to
   `mcpServers` so Claude can call decision-support tools directly.

4. **Notification setup** — prompts for Telegram bot token + chat ID, or
   skip and use desktop only.

### `arbiter doctor`

Diagnostics. Checks:

- Binary on PATH, version current
- `~/.claude/settings.json` hooks present + executable
- `ANTHROPIC_API_KEY` env var set (or alternative auth)
- `.arbiter/` exists in cwd (warns if not — falls back to default rules)
- Notification channel reachability (Telegram bot DM test, desktop notify-send test)
- Claude Code CLI version compatible

Output is human-readable + machine-readable (`--json`).

## Configuration directories

| Path | Purpose |
|---|---|
| `~/.claude/settings.json` | Hook + MCP registration (Claude Code's file) |
| `~/.config/godx-arbiter/config.yaml` | Global arbiter config (notification channels, fallback rules.md) |
| `~/.config/godx-arbiter/skills/` | Globally-available skills referenced by `rules.md` |
| `~/.local/share/godx-arbiter/events.jsonl` | Eventlog (decisions made) |
| `<project>/.arbiter/` | Per-project config (rules.md + policy.yaml + skills/) |

## Environment variables

| Var | Purpose |
|---|---|
| `ANTHROPIC_API_KEY` | API key for the arbiter's agent. Required for slow-path |
| `GODX_ARBITER_HOME` | Override `~/.config/godx-arbiter` location |
| `GODX_ARBITER_LOG_LEVEL` | `debug` / `info` / `warn` / `error` (default `info`) |
| `GODX_ARBITER_TELEGRAM_TOKEN` | Telegram bot token (alternative to config.yaml) |
| `GODX_ARBITER_TELEGRAM_CHAT_ID` | Telegram chat ID for escalations |
| `GODX_ARBITER_DISABLED` | If `1`, arbiter approves everything (kill switch for emergencies) |

## Uninstall

```bash
arbiter uninstall          # removes hooks from ~/.claude/settings.json, leaves .arbiter/ alone
npm uninstall -g godx-arbiter   # if installed via npm
rm ~/.local/bin/arbiter         # if manual
```

## Troubleshooting

**Hook not firing**: `arbiter doctor` confirms hook is registered. If
yes but still not firing, check Claude Code is reading
`~/.claude/settings.json` (not a project-local override).

**Slow decisions**: Set `agent_model: claude-haiku-4-5-20251001` in
`rules.md` front matter. If still slow, check `ANTHROPIC_API_KEY` rate
limits.

**Notifications not arriving**: `arbiter doctor --notify-test` sends a
test message via each configured channel.

**Want to disable temporarily**: `export GODX_ARBITER_DISABLED=1` in
your shell. Or comment out hooks in `~/.claude/settings.json`.
