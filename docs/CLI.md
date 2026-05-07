# CLI reference

Authoritative reference for every `arbiter` subcommand. For higher-level
how-to, see [INSTALL.md](INSTALL.md).

## Synopsis

```
arbiter <command> [flags] [args...]
```

Commands:

| Command | Purpose |
|---|---|
| [`hook`](#arbiter-hook) | Lifecycle entrypoint for Claude Code (stdin JSON → stdout decision) |
| [`init`](#arbiter-init) | Scaffold `.arbiter/` and register hooks in `~/.claude/settings.json` |
| [`uninstall`](#arbiter-uninstall) | Remove arbiter hooks + MCP entries from `~/.claude/settings.json` |
| [`doctor`](#arbiter-doctor) | Diagnose installation, env, project config, channels |
| [`auth`](#arbiter-auth) | Manage provider API keys (OS keychain) |
| [`mcp`](#arbiter-mcp) | Run the stdio MCP server (decision-support tools) |
| [`proxy`](#arbiter-proxy) | Run the local LLM proxy (Mode B integration) |
| [`usage`](#arbiter-usage) | Token + cost summary from the usage ledger |
| [`logs`](#arbiter-logs) | Tail or filter the decision eventlog |
| [`explain`](#arbiter-explain) | Replay a past decision with full agent rationale |
| [`version`](#arbiter-version) | Print version |
| `help`, `--help`, `-h` | This message |

Common flags exit semantics:
- `0` — success
- `1` — operational error (parse, network, etc.)
- `2` — usage error (bad flags, missing args)

---

## `arbiter hook`

```
arbiter hook <pretool|posttool|notification|stop>
```

Reads a JSON event from stdin, runs the decide pipeline, writes the
decision JSON on stdout. Designed to be the `command` in a Claude Code
hook block in `~/.claude/settings.json`.

The cardinal rule (ADR-005): **arbiter must never break a calling
session.** Any internal failure causes the rules.md `on_error` policy
to drive the response (default `approve`).

Input schema (Claude Code's PreToolUse shape; non-Claude-Code CLIs are
normalized via adapters):

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "cwd": "/abs/path",
  "hook_event_name": "PreToolUse",
  "tool_name": "Bash",
  "tool_input": {"command": "rm -rf node_modules"}
}
```

Output schema (modern `hookSpecificOutput.permissionDecision`, also
honored by Codex 0.128+):

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow",
    "permissionDecisionReason": "read-only system command"
  },
  "metadata": {
    "path": "fast-path",
    "rule_type": "allow",
    "rule_index": 3,
    "duration_ms": 4,
    "session_id": "..."
  }
}
```

`metadata.path` values:
- `fast-path` — decided by `policy.yaml` regex
- `slow-path` — decided by the LLM agent
- `escalation` — agent asked + user replied via notify channel
- `fallback` — agent timed out / errored, applied `on_timeout` /
  `on_error` policy
- `agent-stub` — slow-path skipped (no API key); approved per fail-open
- `kill-switch` — `GODX_ARBITER_DISABLED=1` or rules.md
  `enabled: false`

---

## `arbiter init`

```
arbiter init [--dir PATH] [--template balanced|strict|sandbox]
             [--interactive | --non-interactive]
             [--force] [--skip-hooks] [--skip-mcp]
```

Scaffolds `.arbiter/rules.md` + `.arbiter/policy.yaml` +
`.arbiter/skills/` and merges arbiter hook + MCP entries into
`~/.claude/settings.json` (with timestamped backup).

### Interactive wizard (default)

When stdin is a TTY and no `--template` is set, `init` runs a wizard
that asks about the project + risk tolerance and writes a personalized
`rules.md`. Sample run:

```
godx-arbiter init wizard — let's tailor the rules to this project.
Press ENTER to accept the default in [brackets].

Project name [famgia-admin]:
Project type (production|sandbox|library|tool) [production]
  hint: production = strict; sandbox = lax; library/tool = balanced
> production
Languages (comma-separated, e.g. go,ts,python): go,ts
Slow-path agent model (haiku|sonnet|opus) [haiku]
On internal arbiter error (approve|deny) [approve]
  hint: approve = fail-open (recommended); deny = paranoid
> deny
On agent timeout (approve|deny|escalate) [deny]

── Decisions ─────────────────────────────────────────────────────
Built-in safe denies are always on. Add project-specific rules below.

Extra DENY rules (one per line, blank to finish)
  - backend/internal/config/values.go: PR review only
  - backend/migrations/*.sql: never edit applied files
  -
Extra AUTO-APPROVE rules (one per line, blank to finish)
  - frontend/src/i18n/locales/*.json: small string changes
  -
Extra ESCALATE rules (one per line, blank to finish)
  - New dependencies in go.mod / package.json
  -

── Notifications ─────────────────────────────────────────────────
Use Telegram for escalations? (y/n) [n]: y
  → after init, run: arbiter auth set telegram
    and: export GODX_ARBITER_TELEGRAM_CHAT_ID=<your chat id>
Quiet hours (HH:MM-HH:MM, suppresses Telegram, blank = none): 22:00-07:00

Register the MCP server in ~/.claude/settings.json? (y/n) [y]: y
```

The result: a `rules.md` whose front matter reflects the wizard
answers (`agent_model`, `on_error`, `notify_channels`, `quiet_hours`)
and whose body merges the built-in `## Auto-approve / Deny / Escalate`
sections with the user's project-specific lines.

### Templates (non-interactive fallback)

- `balanced` (default when fallback fires) — production defaults: deny
  destructive + secret patterns, allow read-only, escalate sudo
- `strict` — default-deny posture; only `Read/Glob/Grep` auto-approves
- `sandbox` — default-approve; only catastrophic patterns block

### Flags

| Flag | Effect |
|---|---|
| `--dir PATH` | Scaffold somewhere other than cwd |
| `--template NAME` | Skip the wizard; use a canned template |
| `--non-interactive` | Force template fallback (good for CI / scripts) |
| `--force` | Overwrite existing `rules.md` / `policy.yaml` |
| `--skip-hooks` | Don't touch `~/.claude/settings.json` |
| `--skip-mcp` | Register hooks but not the MCP server |

Environment overrides:

- `GODX_ARBITER_FORCE_WIZARD=1` — run the wizard even when stdin is
  piped (useful for integration tests).

Without `--force`, existing files are left in place with a notice.

---

## `arbiter uninstall`

```
arbiter uninstall [--dry-run]
```

Removes arbiter's hook + MCP entries from `~/.claude/settings.json`.
Project `.arbiter/` directories are intentionally **not** touched.

A timestamped backup is always written next to settings.json before
mutation, even with empty changes.

---

## `arbiter doctor`

```
arbiter doctor [--notify-test] [--json]
```

Reports:

- Binary version + path
- Environment variables (set/unset, masked for `ANTHROPIC_API_KEY`)
- Project detection result (root, rules.md presence, policy rule
  counts, warnings)

Flags:
- `--notify-test` — sends a live test message to every available
  notification channel and reports per-channel result
  (`✓ delivered` / `⚠ delivered (no reply)` / `✗ unavailable` / `✗ error`)
- `--json` — emits a stable machine-readable schema instead of the
  human report (see [`docs/EVENTLOG.md`](EVENTLOG.md) for similar
  schema discipline)

Exit code is non-zero when warnings or hard errors are present.

---

## `arbiter auth`

```
arbiter auth set <provider> [<value>]
arbiter auth get <provider>
arbiter auth list
arbiter auth delete <provider>
```

Manages provider API keys. Resolution chain (first hit wins):

1. Process env var (e.g. `ANTHROPIC_API_KEY`)
2. OS keychain entry under service `godx-arbiter`
3. `$GODX_ARBITER_HOME/credentials` plain-text fallback (last resort)

Providers: `anthropic`, `openai`, `google`, `telegram`.

`set` without `<value>` reads from stdin so the credential never
appears in shell history. With `<value>`, it goes through argv and
into your shell's history — only useful in scripts that you've
secured separately.

---

## `arbiter mcp`

```
arbiter mcp
```

Starts the stdio MCP server (JSON-RPC 2.0). Speaks the subset:

- `initialize`
- `notifications/initialized`
- `tools/list`
- `tools/call`
- `ping`

Tools exposed match `internal/tools.DefaultRegistry()`:
`analyze_risk`, `check_rule`, `lookup_history`, `read_file`,
`list_recent_actions`, `get_project_meta`, `escalate_to_user`,
`delegate_to`. See [MCP_TOOLS.md](MCP_TOOLS.md).

Register in `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "godx-arbiter": { "command": "arbiter", "args": ["mcp"] }
  }
}
```

`arbiter init` does this automatically unless `--skip-mcp` is passed.

---

## `arbiter proxy`

```
arbiter proxy [--addr HOST:PORT] [--cli LABEL]
```

Starts the local LLM proxy (Mode B in [MULTI_CLI.md](MULTI_CLI.md)).
Default `:7777`.

Provider endpoints:
- `POST /v1/messages` → Anthropic
- `POST /v1/chat/completions` → OpenAI
- `POST /v1/responses` → OpenAI (Responses API)
- `POST /v1beta/...` → Gemini
- `GET /healthz` → status

Behavior:
- **PreForward**: classifies the request, applies `## Model routing`
  rules from rules.md, may rewrite `model`. Routing decisions surface
  as `X-Arbiter-Routed-From` / `X-Arbiter-Routed-To` /
  `X-Arbiter-Routed-Reason` headers on the response.
- **PostResponse** (non-streaming): runs `policy.yaml` fast-path
  against any tool_use blocks; rewrites denied calls to refusal text
  blocks (Anthropic) or refusal `arguments` payloads (OpenAI).
- **StreamTransform** (`text/event-stream`): same gating, applied
  chunk-by-chunk for Anthropic + OpenAI. Other providers stream
  through unchanged.
- **Token accounting**: every response is logged to the usage ledger
  (input/output tokens, estimated cost via known model pricing).

`--cli LABEL` tags the usage ledger so you can split costs by CLI.

---

## `arbiter usage`

```
arbiter usage [--today] [--since RFC3339]
```

Summarizes the JSONL ledger at
`$GODX_ARBITER_HOME/usage.jsonl` (or `~/.local/share/godx-arbiter/usage.jsonl`)
into per-(session, cli, model) totals.

```
session abc          — claude-code  — claude-sonnet-4-6              — in=12340 out=890 — $0.0419
session abc          — claude-code  — claude-haiku-4-5-20251001      — in=4832 out=535  — $0.0075
─────────────────────────────────────────────────────────────────────────────
Total: in=17172 out=1425 — $0.0494
```

Costs are estimated from the table in
[`internal/proxy/translate/translate.go:costPerMTokens`](https://github.com/godx-team/godx-arbiter/blob/main/internal/proxy/translate/translate.go).
Unknown models contribute $0 — under-bills rather than fabricates.

---

## `arbiter logs`

```
arbiter logs [--tail] [-n N] [--session ID] [--tool NAME]
             [--decision allow|deny] [--since RFC3339] [--json]
```

Reads the decision eventlog (`events.jsonl`).

Without `--tail`, prints the last `N` matches (default 20). With
`--tail`, follows the file and streams new entries.

Filters can compose; all are AND'd. Output format:

```
2026-05-06 23:23:00  allow     Edit        real-1  {"file_path":"main.go",...}
2026-05-06 23:28:53  deny      Bash        real-2  {"command":"git push --force origin staging"}
```

`--json` prints raw JSONL (suitable for `jq` pipelines).

See [EVENTLOG.md](EVENTLOG.md) for the full schema.

---

## `arbiter explain`

```
arbiter explain <session-id> [event-id] [-v]
arbiter explain --last [-v]
```

Replays a past decision with full rationale: timestamp, project, tool,
input summary, decision path, agent metadata. With `-v`, prints the
agent's tool-use trace and final ARBITER_DECISION text.

Use cases:
- Post-mortem after a surprising deny
- Demoing how the agent reasoned through a tricky case
- Verifying tool calls landed correctly during development

---

## `arbiter version`

```
arbiter version
```

Prints `godx-arbiter <semver-or-commit-sha>`. The version is baked at
build time via `-ldflags "-X main.version=..."`.
