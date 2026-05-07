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

## Use cases & best practices

The same tools serve very different consumers. Here's how to think about
them in practice.

### Use case 1 — Claude Code asks itself "is this safe?"

Pattern: a user prompts Claude to do something risky ("clean up
node_modules", "rebase main onto staging"). Before the agent commits,
it calls `analyze_risk` to get a numeric score, then `lookup_history`
to see how similar past calls were decided.

```
User: clean up the build artifacts
Claude (internal): I'll Bash 'rm -rf node_modules' …
  → calls mcp__godx-arbiter__analyze_risk({tool: "Bash",
       input: {command: "rm -rf node_modules"}, cwd})
    ← score 0.3, category destructive-reversible, reversibility easy
  → calls mcp__godx-arbiter__lookup_history({tool: "Bash",
       pattern: "rm -rf node_modules", limit: 3})
    ← three prior approves with reason "regenerable"
Claude proceeds with the Bash call.
```

**Best practice**: gate every destructive tool call on `analyze_risk`,
even when arbiter's own fast-path would pass it. The cost is 0 (it's
heuristic-only) and it catches naming variations the regex misses.

### Use case 2 — agent gathers context before deciding

Pattern: the slow-path agent receives an `Edit` to a file it's never
seen. It needs to know what's in the file before deciding.

```
agent → read_file({project_root, path: "backend/internal/config/values.go", max_bytes: 8192})
agent → check_rule({project_root, query: "deployment-critical"})
agent → get_project_meta({project_root})  # branch, recent commits
→ ARBITER_DECISION: deny — values.go is in the deployment-critical
  list per rules.md §Deny; PR review required.
```

**Best practice**: prefer `read_file` over guessing from `tool_input`
alone. Files change; rules.md describes intent. Both views matter.

### Use case 3 — escalation as a fallback, not a default

Pattern: the agent has an opinion but the action's blast radius is
high enough to want a human ack.

```
agent → analyze_risk → 0.85 (history-rewriting)
agent → lookup_history → no precedent in this session
agent → escalate_to_user({
  question: "Force push to staging will rewrite shared history. Approve?",
  options: ["approve", "deny"],
  context: {tool: "Bash", command: "git push --force origin staging"},
  channel: "telegram",
  timeout_seconds: 60
})
→ user replies "approve" → ARBITER_DECISION: approve
```

**Best practice**: only call `escalate_to_user` after `analyze_risk` +
`lookup_history` + `check_rule` have all been consulted. Repeatedly
pinging the user is the fastest way to make them ignore future
notifications.

### Use case 4 — cross-CLI handoff for a sub-task

Pattern: Claude Code is mid-conversation but the next step is something
Codex is better at (large codegen, OpenAI-only model).

```
Claude → mcp__godx-arbiter__delegate_to({
  cli: "codex",
  task: "implement the migration script per the plan above",
  context: {plan_id: "...", files: [...]},
  budget_tokens: 20000,
  timeout_seconds: 180
})
→ codex runs non-interactively, returns its final output
Claude reads the output, integrates, continues.
```

**Best practice**: pass the task as plain English plus a small,
self-contained `context` object. Avoid trying to ship the entire
parent conversation — the delegate doesn't need it.

### Use case 5 — debugging a surprising deny

Pattern: a user complains "why did arbiter block X?" Run `arbiter
explain --last -v` to read the agent's full transcript.

The same tools that drove the original decision are visible in the
transcript, so you can see *exactly* what context the agent had —
which `analyze_risk` score it saw, which `read_file` returned, which
`check_rule` matched. No guessing.

**Best practice**: when adjusting `rules.md`, replay one or two recent
events with `--v` first. The trace usually points at the rule that
needs sharpening.

### Anti-patterns

| Don't | Why |
|---|---|
| Call `analyze_risk` with hand-crafted inputs to "test" rules | The risk score is a heuristic, not a contract. Test with real `tool_input` from `arbiter logs --tool <X>`. |
| Put secrets in `escalate_to_user.context` | The context shows up in Telegram messages, desktop notifications, and the eventlog. |
| Use `delegate_to` to avoid arbiter's policy | The delegated CLI also has hooks if you've installed arbiter for it. Trying to launder a denied call through `delegate_to` won't work and shouldn't. |
| Loop tool calls without progress | The agent has a `max_agent_iterations` cap (default 10). Burning iterations on speculative `lookup_history` calls hits the cap before the decision lands. |

## Composition recipes

A few well-tested call sequences for common decision shapes:

**"Safe destructive operation in a regenerable directory"**
```
analyze_risk → check category == "destructive-reversible"
analyze_risk → check reversibility ∈ {trivial, easy}
→ approve
```

**"Edit to a config file"**
```
read_file(path)
check_rule(keyword extracted from path) — e.g. "config", "secret"
analyze_risk(tool, input)
if any signal points high-risk → escalate
else if low-risk + matches an explicit auto-approve → approve
else → deny with reason
```

**"Bash command not in allow regex"**
```
analyze_risk → category, score
lookup_history(pattern: command head) — has the user approved this shape before?
if score < 0.3 AND past approves > 0 → approve
if score > 0.6 → deny with reason
otherwise → escalate
```

These are not hard rules — they're the patterns the slow-path agent
already follows when its system prompt is well-tuned. Encoding them in
`rules.md` (or in a project skill file) makes the agent's behavior
predictable across sessions.

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

`arbiter mcp` runs a stdio MCP server speaking JSON-RPC 2.0. Register
in `~/.claude/settings.json`:

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

### Wire format

One JSON object per line on stdin/stdout. Each request is matched to a
response by `id`. Notifications (no `id`) get no response.

We implement the load-bearing subset of the MCP spec:

| Method | Direction | Purpose |
|---|---|---|
| `initialize` | client → server | Capability handshake; returns `protocolVersion: 2024-11-05` and `serverInfo` |
| `notifications/initialized` | client → server | Sent after init; arbiter is a no-op recipient |
| `tools/list` | client → server | List available tools with input schemas |
| `tools/call` | client → server | Invoke a tool with arguments |
| `ping` | client → server | Liveness check |

Anything else returns `-32601 method not found`. Resources, prompts,
sampling, roots, and the full server-info dance are out of scope —
they're not load-bearing for tool use, and the calling clients
(Claude Code, MCP Inspector) gracefully degrade when capabilities are
missing.

### Initialize handshake

```jsonl
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
```

Response:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "serverInfo": {"name": "godx-arbiter", "version": "0.1.0"},
    "capabilities": {"tools": {}}
  }
}
```

### Listing tools

```json
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
```

Response (excerpt):

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "tools": [
      {
        "name": "analyze_risk",
        "description": "Estimate risk of a proposed tool call. Returns score (0..1), category, ...",
        "inputSchema": {
          "type": "object",
          "properties": {
            "tool":  {"type": "string"},
            "input": {"type": "object"},
            "cwd":   {"type": "string"}
          },
          "required": ["tool"]
        }
      },
      ...
    ]
  }
}
```

### Calling a tool

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "analyze_risk",
    "arguments": {
      "tool": "Bash",
      "input": {"command": "rm -rf node_modules"},
      "cwd": "/home/u/famgia/admin"
    }
  }
}
```

Success response (the result is wrapped in a `content` array per the
MCP spec; our tool output is one `text` block carrying the JSON
output):

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"score\":0.3,\"category\":\"destructive-reversible\",\"concerns\":[\"destructive but path is regenerable\"],\"reversibility\":\"easy\",\"blast_radius\":\"single-directory\"}"
      }
    ]
  }
}
```

### Tool errors

Tool errors don't surface as JSON-RPC errors — that would terminate
the model's reasoning. Instead the result includes `isError: true`
and a `text` block with the error message, so the model can react and
retry:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [{"type": "text", "text": "open /no/such/file: no such file or directory"}],
    "isError": true
  }
}
```

JSON-RPC errors (`-32xxx`) are reserved for transport-level problems
(parse error, unknown method, malformed params).

### Trying it locally

```bash
# pipe a session in
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | arbiter mcp

# use the official MCP Inspector for richer interaction
npx @modelcontextprotocol/inspector arbiter mcp
```

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

## Implementation notes

### Output cap

Every tool's output is bounded:

- `read_file` → 8 KiB by default (`max_bytes` argument tunes it)
- `delegate_to` → 16 KiB; truncated output sets `truncated: true`
- `lookup_history` → caller-provided `limit` (default 5)
- `analyze_risk`, `check_rule`, `get_project_meta`, `list_recent_actions` → bounded by their natural shape

Over-cap is a guardrail — we don't want a single tool call to pull a
50 MB log into the agent's context.

### Determinism

`analyze_risk`, `check_rule`, `read_file`, `get_project_meta` are
deterministic given the same inputs (and same on-disk state). The
others depend on external state (eventlog for history, OS notify
channels for escalation, subprocess output for delegate_to).

### Concurrency

The registry is concurrent-safe via an internal `sync.RWMutex`.
Tools are stateless (no in-tool state across `Execute` calls), so
concurrent invocations are safe. The MCP server processes requests
serially per stdio connection — Claude Code rarely pipelines anyway.

## Future tools (not yet planned in detail)

- `dry_run` — simulate the proposed tool call in a sandbox and report
  effects
- `diff_preview` — for Edit/Write, show diff and ask agent to assess
- `notify_team` — send broadcast for team-wide changes
- `git_blame` — surface who last touched the file the agent's about to edit
- `file_history` — recent commits touching a path
