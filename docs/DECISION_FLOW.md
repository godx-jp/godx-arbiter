# Decision flow

The full decide loop, from hook entry to final decision returned to Claude
Code.

## Entry: hook receives action

Claude Code spawns `arbiter hook pretool` and writes JSON to stdin:

```json
{
  "session_id": "abc-123",
  "transcript_path": "~/.claude/projects/.../session.jsonl",
  "cwd": "/home/u/famgia/admin",
  "hook_event_name": "PreToolUse",
  "tool_name": "Bash",
  "tool_input": {
    "command": "rm -rf node_modules",
    "description": "clean install"
  }
}
```

Arbiter reads stdin, parses, then runs the decide pipeline.

## Pipeline

```
                    ┌────────────────────────┐
                    │ 1. detect project      │
                    │  (walk up from cwd)    │
                    └───────────┬────────────┘
                                │
                                ▼
                    ┌────────────────────────┐
                    │ 2. load config         │
                    │  rules.md + policy.yaml│
                    │  + skills referenced   │
                    └───────────┬────────────┘
                                │
                                ▼
                    ┌────────────────────────┐
                    │ 3. fast-path eval      │
                    │  policy.yaml regex     │
                    └───────────┬────────────┘
                                │
                  ┌─── match? ──┴── no match?
                  │                       │
                  ▼                       ▼
          ┌──────────────┐       ┌────────────────┐
          │ return       │       │ 4. spawn agent │
          │ allow/deny   │       │   (LLM)        │
          └──────────────┘       └────────┬───────┘
                                          │
                                          ▼
                                ┌────────────────────┐
                                │ 5. agent tool loop │
                                │  decide / escalate │
                                └────────┬───────────┘
                                         │
                              ┌──────────┼──────────┐
                              ▼          ▼          ▼
                          approve      deny       ask
                              │          │          │
                              │          │          ▼
                              │          │   ┌─────────────┐
                              │          │   │ notify user │
                              │          │   │ wait reply  │
                              │          │   │ (timeout T) │
                              │          │   └──────┬──────┘
                              │          │          │
                              │          │   ┌──────┴──────┐
                              │          │   │ user reply  │
                              │          │   │ or fallback │
                              │          │   └──────┬──────┘
                              ▼          ▼          ▼
                        ┌──────────────────────────────┐
                        │ 6. return decision to Claude │
                        │ 7. eventlog append           │
                        └──────────────────────────────┘
```

## Step 1 — Project detection

`internal/projectfind` walks upward from `cwd`:

```
/home/u/famgia/admin/cmd/something/   ← cwd
/home/u/famgia/admin/                  ← .arbiter/ found here ✓
/home/u/famgia/                        (stop)
/home/u/                               (stop)
```

If no `.arbiter/` is found, fall back to:
1. `$XDG_CONFIG_HOME/godx-arbiter/default-rules.md`
2. Built-in conservative default (deny destructive, allow read-only,
   escalate everything else)

The fallback is announced in the decision reason so the user knows.

## Step 2 — Config load

```
.arbiter/
├── rules.md       # parsed: front-matter (yaml) + body (markdown)
├── policy.yaml    # parsed: schema-validated YAML
└── skills/*.md    # loaded on demand if rules.md references them
```

Cache: parsed configs cached by mtime. Re-parsed only on file change. This
keeps hook latency negligible (~5ms steady state).

## Step 3 — Fast-path evaluation

`policy.yaml` example:

```yaml
allow:
  - tool: Bash
    pattern: '^(ls|cat|head|tail|grep|rg|fd|git status|git log|git diff)\b'
  - tool: Read
  - tool: Glob
  - tool: Grep

deny:
  - tool: Bash
    pattern: '\brm\s+-rf\b'
    reason: rules.md forbids rm -rf

  - tool: Bash
    pattern: '\bgit push.*--force\b'
    reason: rules.md forbids force push
```

Evaluation: in declaration order. First match wins. `deny` and `allow`
share the same list ordering — author writes most-specific first.

If no rule matches → fall through to slow-path.

## Step 4 — Agent spawn

Build the system prompt from:

```
[rules.md body]

[--- referenced skills ---]
{skills/review-before-merge.md content}
{skills/safe-bash-allowlist.md content}

[--- action under review ---]
Tool: Bash
Input: {"command": "rm -rf node_modules", "description": "clean install"}
Cwd:   /home/u/famgia/admin
Session: abc-123
```

User message:

```
Decide: approve, deny, or ask the human user.
Use the tools provided to gather context if needed.
End with one of:
  ARBITER_DECISION: approve
  ARBITER_DECISION: deny — <reason>
  ARBITER_DECISION: ask — <question>
```

Tools registered:
- `analyze_risk(action)` — risk score + reasoning
- `check_rule(rule_id_or_keyword)` — fetch specific rules.md section
- `lookup_history(action_pattern)` — past similar decisions from eventlog
- `read_file(path)` — read project files for context
- `escalate_to_user(question, options, channel?, timeout?)` — sends notification

## Step 5 — Agent tool loop

Standard Anthropic tool-use loop:

```
while not done:
    response = anthropic.messages(messages, tools=arbiter_tools)
    if response.stop_reason == "end_turn":
        parse final decision from text
        break
    for tool_use in response.content:
        result = execute_tool(tool_use)
        messages.append(tool_use_result)
```

Bounded by:
- Max 10 tool iterations (configurable)
- Hard timeout (default 30s; configurable per project)
- Max input tokens (avoid runaway context)

If timeout / max iters hit → fallback per `rules.md` (default = deny + log).

## Step 6 — Escalation (when agent picks "ask")

The agent calls `escalate_to_user`:

```json
{
  "question": "Claude wants to delete node_modules in admin/. Approve?",
  "options": ["approve", "deny"],
  "context": {
    "tool": "Bash",
    "command": "rm -rf node_modules",
    "cwd": "/home/u/famgia/admin"
  },
  "channel": "telegram",
  "timeout_seconds": 60
}
```

Notify dispatcher:
1. Sends message via configured channel(s)
2. Spawns a watcher that waits for a reply (Telegram: webhook callback;
   desktop: action button click)
3. On reply → returns user's choice to the agent
4. On timeout → returns `{"timeout": true}` and the agent applies the
   `rules.md` timeout policy (default: deny)

## Step 7 — Return + log

Decision JSON returned to Claude Code on stdout:

```json
{
  "decision": "block",
  "reason": "rules.md §destructive: rm -rf outside /tmp is denied",
  "metadata": {
    "path": "fast-path",
    "matched_rule": "policy.yaml:deny[0]",
    "duration_ms": 4
  }
}
```

For `approve`: `{"decision": "approve"}` (or omit `decision` for default-allow).

For `ask` returned to Claude Code: not directly supported — instead, after
escalation the arbiter returns approve/block based on user reply.

Eventlog entry appended to `~/.local/share/godx-arbiter/events.jsonl`:

```json
{
  "ts": "2026-05-05T20:33:00Z",
  "session_id": "abc-123",
  "project": "/home/u/famgia/admin",
  "tool": "Bash",
  "input_summary": "rm -rf node_modules",
  "path": "fast-path",
  "decision": "deny",
  "duration_ms": 4
}
```

This log feeds `lookup_history` and `arbiter explain`.

## Latency budget

| Path | Target p50 | Target p99 |
|---|---|---|
| Fast-path match | < 10ms | < 30ms |
| Slow-path no escalation | < 1.5s | < 5s |
| Slow-path with escalation | bounded by user response | timeout |

Slow-path uses Haiku by default for low-latency decisions. Per-rule
override to Sonnet/Opus for critical sections.

## Failure modes

| Failure | Behavior |
|---|---|
| `rules.md` parse error | Log error, fall back to default rules, continue |
| `policy.yaml` parse error | Log error, skip fast-path, go to slow-path |
| Anthropic API error | Retry once with backoff; on second fail → fallback |
| Network down (escalation) | Timeout → fallback policy |
| Hook stdin malformed | Return `{"decision":"approve"}` (don't break Claude); log |

The cardinal rule: **arbiter must never break a Claude Code session**. On
internal error, default to approve + log. The user trades safety for
availability — failing closed would block all work if arbiter has a bug.

(This is configurable: set `on_error: deny` in rules.md if you prefer
fail-closed semantics.)

## End-to-end traces

### Trace 1 — fast-path deny

```
$ echo '{"session_id":"abc","cwd":"/home/u/proj","hook_event_name":"PreToolUse",
        "tool_name":"Bash","tool_input":{"command":"rm -rf /etc/foo"}}' \
   | arbiter hook pretool
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "rm -rf of system-critical root directory"
  },
  "metadata": {
    "duration_ms": 0,
    "has_policy": true,
    "matched_pattern": "\\brm\\s+-rf\\s+/(etc|usr|var|...)\\b",
    "path": "fast-path",
    "rule_index": 0,
    "rule_type": "deny",
    "session_id": "abc",
    "tool": "Bash"
  }
}
```

Cost: $0 (no LLM call). Latency: < 1ms.

### Trace 2 — slow-path approve

```
$ echo '{"session_id":"def","cwd":"/home/u/proj","hook_event_name":"PreToolUse",
        "tool_name":"Edit","tool_input":{"file_path":"main.go","old_string":"x","new_string":"y"}}' \
   | arbiter hook pretool
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow"
  },
  "metadata": {
    "agent_iters": 2,
    "agent_outcome": "approve",
    "duration_ms": 7298,
    "path": "slow-path",
    "rule_type": "default",
    ...
  }
}

$ arbiter explain --last -v
Event 18ad…
  agent:
    model: claude-haiku-4-5-20251001
    iters: 2
    tokens: in=4832 out=535
    tool[0]: read_file       — open main.go: ...
    tool[1]: analyze_risk    → score 0.2, file-edit
    tool[2]: get_project_meta → has_arbiter: true
    final: ...ARBITER_DECISION: approve
```

Cost: ~$0.005 (Haiku). Latency: ~7s.

### Trace 3 — slow-path → ask → escalation timeout → deny

```json
{
  "hookSpecificOutput": {
    "permissionDecision": "deny",
    "permissionDecisionReason": "escalation timed out: Force push to staging will rewrite shared history; should this be approved?"
  },
  "metadata": {
    "agent_iters": 3,
    "agent_outcome": "ask",
    "escalation": "timeout",
    "fallback": "deny",
    "path": "slow-path",
    ...
  }
}
```

Sequence:

1. Tool didn't match `policy.yaml` deny/allow → slow-path.
2. Agent reasoned, called tools, decided **ask** (the rules.md's
   "Escalate" section listed force-push).
3. `escalate_to_user` ran the configured channels:
   - `desktop` → fired but no reply mechanism.
   - `telegram` → not configured (env vars unset).
4. Escalation deadline (`escalation_timeout_seconds`, default 60s) hit
   → `Reply{Timeout:true}`.
5. Fallback per rules.md `on_timeout: deny` → final outcome **deny**.

The agent's question text is preserved in
`permissionDecisionReason`, so the calling agent (Claude Code) sees
exactly why we said no.

## Streaming via the proxy

Hook mode operates on whole tool calls. **Proxy mode** also operates
on streaming tool calls:

```
upstream SSE
  ↓
event: content_block_start (type=tool_use, id=u1, name=Bash)
event: content_block_delta (input_json_delta partial="...")
event: content_block_delta (input_json_delta partial="...")
event: content_block_stop
  ↓
arbiter assembles input → policy.Eval → deny?
  ↓ if deny
   rewrite the events as a single text block:
     content_block_start (type=text)
     content_block_delta (text="tool refused by godx-arbiter: <reason>")
     content_block_stop
  ↓
client sees a refusal block, can recover with the next user turn.
```

Same pattern for OpenAI's `tool_calls.function.arguments` chunks; the
arguments JSON is rewritten to
`{"arbiter_refused":true,"reason":"..."}` so the calling agent's
function-call handler still resolves the call.

See [`internal/proxy/sse.go`](https://github.com/godx-team/godx-arbiter/blob/main/internal/proxy/sse.go)
and [`internal/proxy/sse_openai.go`](https://github.com/godx-team/godx-arbiter/blob/main/internal/proxy/sse_openai.go)
for the wire-level details.
