# `.arbiter/rules.md` specification

Project-level rules file. Lives at `<project>/.arbiter/rules.md`. The
arbiter agent reads this verbatim into its system prompt — so it doubles
as both human documentation and LLM context.

## Format

```
---
# (optional) YAML front matter — machine-readable knobs
agent_model: claude-haiku-4-5-20251001
timeout_seconds: 30
on_timeout: deny           # approve | deny | escalate
on_error: approve          # approve | deny
notify_channels: [telegram, desktop]
quiet_hours: "22:00-07:00"
---

# Project — <project name>

(optional preamble — context for the agent about what this project is,
who works on it, what kinds of operations are expected. The agent reads
this as background.)

## Auto-approve

(rules the agent should treat as definitely-OK)

## Deny

(rules the agent must enforce as hard blocks)

## Escalate

(situations the agent should ask the user about rather than decide alone)

## Notification policy

(channel, quiet hours, dedup, timeout fallback)

## Custom

(free-form additional rules the agent reads and interprets)
```

## Front matter (optional, all fields optional)

| Key | Type | Default | Notes |
|---|---|---|---|
| `agent_model` | string | `claude-haiku-4-5-20251001` | Model used by the arbiter agent for slow-path decisions |
| `timeout_seconds` | int | `30` | Agent must decide within this; otherwise fallback |
| `on_timeout` | enum | `deny` | What to do if the agent times out |
| `on_error` | enum | `approve` | What to do on internal arbiter errors. **`approve` = fail-open (default, safer for availability); `deny` = fail-closed** |
| `notify_channels` | list | `[telegram, desktop]` | Channels for `escalate_to_user` |
| `quiet_hours` | string | (none) | `"HH:MM-HH:MM"` — desktop-only during this window |
| `escalation_timeout_seconds` | int | `60` | How long to wait for user reply before fallback |
| `max_agent_iterations` | int | `10` | Max tool-loop iterations |
| `enabled` | bool | `true` | If `false`, arbiter approves everything (kill switch) |

## Body sections

The agent reads the entire body. Section names are conventions, not parser
keywords — the agent infers intent from natural language. That said, the
following structure is recommended for clarity and consistency.

### `## Auto-approve`

Things the agent can approve without further analysis. Prefer concrete
patterns; the LLM is good at generalizing.

```markdown
## Auto-approve

- All Read, Glob, Grep tool calls
- Bash commands that are clearly read-only: `ls`, `cat`, `head`, `tail`,
  `grep`, `rg`, `fd`, `git status`, `git log`, `git diff`, `go test`,
  `npm test`
- Edits to test files (`*_test.go`, `*.test.ts`, `__tests__/*`)
- Edits to documentation (`*.md`) outside `docs/api/`
```

### `## Deny`

Hard blocks. The agent must refuse and explain why.

```markdown
## Deny

- `rm -rf` of any path outside `/tmp` or `node_modules`
- `git push --force` to `master` or `main`
- Any modification of `backend/.env`, `backend/.env.production`, or files
  matching `*.pem`, `*.key`, `*credentials*`
- Direct edits to `backend/internal/config/values.go` (deployment-critical;
  changes here go through PR review)
```

### `## Escalate`

Situations to ask the user rather than decide unilaterally.

```markdown
## Escalate

- Any database migration: files matching `backend/migrations/*.sql` or
  `backend/internal/db/migrate_*.go`
- Bash commands that use `sudo`
- New dependencies in `go.mod`, `package.json`, `requirements.txt`
- Operations affecting more than 10 files in a single tool call (Bash
  with multi-file glob, etc.)
- Anything touching `infrastructure/`, `terraform/`, or `k8s/`
```

### `## Notification policy`

```markdown
## Notification policy

- Primary channel: Telegram bot
- Quiet hours (22:00 – 07:00 local): desktop notify-send only,
  no Telegram
- Deduplicate: identical questions within 60s collapse to one notification
- Timeout: 60s — fallback = deny for destructive ops, escalate-then-deny
  otherwise
```

### `## Custom`

Free-form rules. The agent reads and interprets these in context. This is
where natural-language rules shine.

```markdown
## Custom

- If Claude proposes refactoring more than 5 files in a single commit,
  escalate — we prefer atomic commits in this repo.
- The hard-coding rule from CLAUDE.md applies: any literal IP address,
  port, or hostname in `.go` / `.ts` files is a deny. Reference
  `cfg.HostBridgeIP` etc. instead.
- Code under `backend/internal/sandbox/` is currently being refactored
  by Duong (PR #317 series) — escalate any edits there until that branch
  merges.
- Files marked `// CRITICAL` in their first 5 lines: deny outright,
  don't even escalate.
```

## How the agent uses this file

1. Loaded verbatim into the system prompt (truncated if > 30k tokens; rare).
2. Agent treats sections as guidance, not regex. So:
   - "rm -rf outside /tmp" → agent can reason about `rm -rf
     /tmp/foo/bar`, recognize it's inside /tmp, and approve.
   - "more than 5 files" → agent counts.
   - "hardcoded IP" → agent inspects the diff content.
3. Conflicts: more-specific rules win. If unclear, the agent escalates.
4. The agent can call `check_rule(keyword)` to re-read a specific
   section if it needs clarification mid-decision.

## Versioning

`rules.md` is read on every hook invocation (with mtime caching). Edit
freely; changes apply to the next tool call. No daemon to restart.

For audit, commit `rules.md` to your project's git. The arbiter logs
the file's git SHA in eventlog when consulted, so you can trace which
version of the rules approved any given action.

## Anti-patterns

| Don't | Why |
|---|---|
| Make `rules.md` a 2000-line manifesto | Token budget; agent skims; clarity drops |
| Put secrets in `rules.md` | The agent may quote it back to itself in tool calls |
| Use `rules.md` as primary source of truth | Code + tests + CLAUDE.md remain the source of truth; arbiter is a guard rail |
| Encode regex in prose ("must match `^[a-z]+$`") | Use `policy.yaml` for regex; keep `rules.md` for human-LLM communication |

## Minimal example

```markdown
---
on_timeout: deny
notify_channels: [desktop]
---

# Sandbox project — relaxed rules

This is a personal sandbox. Auto-approve almost everything.

## Auto-approve
- Everything except deny list below

## Deny
- `rm -rf /` (obviously)
- Edits outside this directory tree
```

See [examples/rules.md](../examples/rules.md) for an annotated
production-style example.
