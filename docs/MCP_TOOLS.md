# MCP decision-support tools

Tools the arbiter agent uses internally to make decisions, also exposed
via an MCP server (`arbiter mcp`) so any Claude Code session can use them
directly when making its own judgment calls.

## How these are used

**Internal use (slow-path agent):** When `rules.md` doesn't immediately
answer a question, the arbiter agent calls these tools as part of its
reasoning loop, then returns a final decision.

**External use (MCP):** A user's Claude Code session can register
`arbiter mcp` as an MCP server in `~/.claude/settings.json`. Then Claude
itself can call these tools — e.g., "let me check `analyze_risk` before
proposing this change." The arbiter agent gets the same tools so its
reasoning is observable + reusable.

## Tool catalog

### `analyze_risk`

Estimate the risk profile of a proposed action.

```json
{
  "name": "analyze_risk",
  "input": {
    "tool": "Bash",
    "input": {"command": "rm -rf node_modules"},
    "cwd": "/home/u/famgia/admin"
  }
}
```

Returns:

```json
{
  "score": 0.6,
  "category": "destructive-reversible",
  "concerns": [
    "rm -rf is destructive",
    "node_modules is regenerable from package.json — reversible"
  ],
  "reversibility": "easy",
  "blast_radius": "single-directory"
}
```

Implementation: heuristic + lightweight LLM call (Haiku) for fuzzy
classification. Cached by input hash for 1h.

### `check_rule`

Look up a specific section of `rules.md` by keyword or section name.

```json
{
  "name": "check_rule",
  "input": {
    "project_root": "/home/u/famgia/admin",
    "query": "rm -rf"
  }
}
```

Returns the matching section verbatim, plus its line range and source
file SHA. Lets the agent re-read a specific rule mid-decision instead of
re-scanning the whole `rules.md`.

### `lookup_history`

Find similar past decisions in the eventlog.

```json
{
  "name": "lookup_history",
  "input": {
    "tool": "Bash",
    "pattern": "rm -rf node_modules",
    "limit": 5
  }
}
```

Returns recent matches:

```json
{
  "matches": [
    {
      "ts": "2026-04-30T11:22:00Z",
      "decision": "approve",
      "reason": "node_modules is regenerable",
      "session_id": "..."
    },
    ...
  ]
}
```

Useful for consistency: "the user approved this last week, default to
approve again unless rules.md changed."

### `read_file`

Read a project file for context. Subject to project boundary checks
(can't escape the project root unless rule explicitly allows).

```json
{
  "name": "read_file",
  "input": {
    "path": "package.json",
    "max_bytes": 8192
  }
}
```

Returns text content (UTF-8 only; binary refused). Used to inspect
context like "is `node_modules` already gitignored?" or "what's in this
file Claude wants to edit?"

### `escalate_to_user`

Send a question to the user via configured notification channel. Blocks
until reply or timeout.

```json
{
  "name": "escalate_to_user",
  "input": {
    "question": "Approve removing node_modules in famgia/admin?",
    "options": ["approve", "deny"],
    "context": {
      "tool": "Bash",
      "command": "rm -rf node_modules",
      "rationale": "Claude says it's for a clean install"
    },
    "channel": "telegram",
    "timeout_seconds": 60
  }
}
```

Returns:

```json
{
  "reply": "approve",
  "via": "telegram",
  "elapsed_ms": 8400,
  "user": "@duong"
}
```

On timeout: `{"timeout": true, "elapsed_ms": 60000}` — agent then applies
`on_timeout` policy from `rules.md` front matter.

### `list_recent_actions`

(Diagnostic tool) Recent tool calls in the current session. Useful for
"is this part of a multi-step refactor I already approved?"

```json
{
  "name": "list_recent_actions",
  "input": {
    "session_id": "abc-123",
    "limit": 20
  }
}
```

### `get_project_meta`

Returns project context — name, recent commits, current branch, presence
of CLAUDE.md, etc.

```json
{
  "name": "get_project_meta",
  "input": {
    "project_root": "/home/u/famgia/admin"
  }
}
```

Returns:

```json
{
  "name": "famgia-admin",
  "branch": "feat/admin-delegate-to-sandbox-service",
  "recent_commits": ["2301987 feat(admin): Phase 3 ..."],
  "has_claude_md": true,
  "languages": ["go", "ts"],
  "git_dirty": false
}
```

## MCP transport

`arbiter mcp` runs a stdio MCP server. Register in
`~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "godx-arbiter": {
      "command": "arbiter",
      "args": ["mcp"]
    }
  }
}
```

Now Claude can invoke `mcp__godx-arbiter__analyze_risk` etc. in any
session.

## Tool design principles

1. **Observable** — every tool call is logged; the agent's reasoning
   trail is reconstructable via `arbiter explain`.
2. **Bounded** — every tool has a hard max output size. No tool can
   pull a 100MB file into context.
3. **Cheap** — the agent runs on Haiku by default. Tools that internally
   call an LLM use Haiku too. Total decision cost target: < $0.001 per
   decision.
4. **Side-effect-free** — except `escalate_to_user`, no tool mutates
   state. Safe to retry.

## Adding a new tool

1. Implement in `internal/tools/<name>.go` with this interface:

   ```go
   type Tool interface {
       Name() string
       Description() string
       InputSchema() map[string]any
       Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
   }
   ```

2. Register in `internal/tools/registry.go`.
3. Tool is automatically available to the arbiter agent and via MCP
   server with no further wiring.
4. Add docs entry to this file.

## Future tools (not yet planned in detail)

- `dry_run` — simulate the proposed tool call in a sandbox and report
  effects
- `diff_preview` — for Edit/Write, show diff and ask agent to assess
- `notify_team` — send broadcast for team-wide changes
