# Eventlog schema

Every decision arbiter makes is appended to a JSONL eventlog. The log
feeds three consumers:

- The `lookup_history` MCP tool — past similar decisions for
  consistency
- [`arbiter explain`](CLI.md#arbiter-explain) — replay
- [`arbiter logs`](CLI.md#arbiter-logs) — tail / filter

## Location

| Path | Source |
|---|---|
| `$GODX_ARBITER_LOG_PATH` | env override (highest priority) |
| `$GODX_ARBITER_HOME/events.jsonl` | when `GODX_ARBITER_HOME` is set |
| `$XDG_DATA_HOME/godx-arbiter/events.jsonl` | XDG systems |
| `~/.local/share/godx-arbiter/events.jsonl` | default |

The directory is created on first write. The file grows append-only;
arbiter never rotates it (use `logrotate`, `cron`, or your tool of
choice if size matters in a long-running setup).

## Schema

One JSON object per line. Fields are forward-compatible — readers
ignore unknown keys, writers add freely.

```json
{
  "ts": "2026-05-06T23:23:00.123456Z",
  "event_id": "18ad1d6c954f8a82-4350ee3b",
  "session_id": "real-1",
  "project": "/tmp/arbiter-real-test",
  "tool": "Edit",
  "input_summary": "{\"file_path\":\"main.go\",\"old_string\":\"package main\",\"new_string\":\"...\"}",
  "path": "slow-path",
  "decision": "allow",
  "reason": "regular file edit, low risk",
  "rules_sha": "0c91a2d…",
  "duration_ms": 7298,
  "agent": {
    "model": "claude-haiku-4-5-20251001",
    "iters": 2,
    "tool_calls": [
      {
        "name": "analyze_risk",
        "input": {"tool":"Edit","input":{...}},
        "output": "{\"score\":0.2,\"category\":\"file-edit\",...}"
      },
      {
        "name": "get_project_meta",
        "input": {"project_root":"/tmp/arbiter-real-test"},
        "output": "{...}"
      }
    ],
    "final": "Based on my analysis…\n\nARBITER_DECISION: approve",
    "tokens": {"input": 4832, "output": 535}
  },
  "extra": {}
}
```

### Field reference

| Field | Type | Notes |
|---|---|---|
| `ts` | RFC3339 UTC | When the decision was made. |
| `event_id` | string | `<unix-nanos-hex>-<random-hex>`. Stable across replays. |
| `session_id` | string | Calling CLI's session id (Claude Code propagates this for free). |
| `project` | string | Absolute path of the project root or "" for "no .arbiter/". |
| `tool` | string | Canonical tool name (Bash / Edit / Write / etc). |
| `input_summary` | string | Stringified tool input, capped at ~240 chars + ellipsis. |
| `path` | enum | `fast-path` \| `slow-path` \| `escalation` \| `fallback` \| `kill-switch` \| `agent-stub`. |
| `decision` | enum | `allow` \| `deny`. (`ask` collapses to one of those after escalation/fallback.) |
| `reason` | string | Human-readable; surfaced to the calling agent on deny. |
| `rules_sha` | string | git SHA of `rules.md` at decision time, when available. |
| `duration_ms` | int | Wall clock from hook entry to decision. |
| `agent` | object | Present only when `path: slow-path`/`escalation`. See below. |
| `extra` | object | Forward-compatibility bucket. Unmapped fields land here. |

### Agent trace

When the slow-path runs, `agent` records the tool-use loop:

| Field | Notes |
|---|---|
| `model` | The model id used (e.g. `claude-haiku-4-5-20251001`). |
| `iters` | Number of model round-trips before deciding. |
| `tool_calls[]` | Each tool the agent invoked, with input + serialized output. Errors are recorded under `err`. |
| `final` | The agent's final assistant text, including the `ARBITER_DECISION:` line. |
| `tokens` | `{input, output}` summed across iterations. Used by [`arbiter usage`](CLI.md#arbiter-usage). |

## Querying

### `lookup_history` (programmatic)

The agent calls it via MCP:

```json
{
  "name": "lookup_history",
  "input": {"tool": "Bash", "pattern": "rm -rf node_modules", "limit": 5}
}
```

Returns the most recent matches first. Filtering is substring on
`input_summary` plus exact match on `tool` / `session`. (Regex is not
supported intentionally — keeps the implementation simple and the
agent's mental model honest.)

### `arbiter logs` (interactive)

```bash
# last 20 of any decision
arbiter logs

# follow live
arbiter logs --tail

# every deny in this session
arbiter logs --session abc-123 --decision deny

# everything since this morning, raw JSONL for jq
arbiter logs --since 2026-05-06T08:00:00Z --json | jq '.tool'
```

### Direct `jq`

```bash
# top tools by count
jq -r .tool ~/.local/share/godx-arbiter/events.jsonl | sort | uniq -c | sort -rn

# slowest slow-path decisions today
jq 'select(.path=="slow-path" and (.ts > "2026-05-06T00:00:00Z"))
    | {ts, tool, duration_ms, iters: .agent.iters}' \
   ~/.local/share/godx-arbiter/events.jsonl | jq -s 'sort_by(-.duration_ms) | .[0:5]'

# how often did the agent escalate vs decide alone?
jq -r 'select(.path | startswith("slow-path") or startswith("escalation"))
       | .path' ~/.local/share/godx-arbiter/events.jsonl | sort | uniq -c
```

## Privacy

The eventlog records the **input summary** (truncated stringified tool
input) and the **agent's reasoning text**. If your tool inputs include
secrets — file contents, env vars passed in `Bash`, etc. — they end up
in the log. Treat the log as you would shell history.

Mitigations:
- Set `GODX_ARBITER_LOG_PATH=/dev/null` in environments where you
  don't want any persistence.
- Per-CLI integrations can pre-sanitize via the proxy's `PreForward`
  hook before the inputs ever reach arbiter.
- The hot path doesn't read the eventlog; deleting the file at any
  time is safe (it'll be re-created on next decision).
