# `.arbiter/policy.yaml` specification

Optional file. Lives at `<project>/.arbiter/policy.yaml`. Used for the
**fast-path** decision: regex patterns evaluated in Go (no LLM cost,
sub-millisecond latency). Useful for the obviously-safe and the
obviously-dangerous — anything in between belongs in `rules.md` for the
agent.

## Schema

```yaml
version: 1                          # required, for forward compat

# Default decision when no rule matches. Default: "agent".
default: agent                      # agent | approve | deny

# Rules are evaluated top-to-bottom. First match wins.
allow:
  - tool: Bash                      # required: tool name (or "*" for all)
    pattern: '^ls\b'                # optional: regex on stringified input
    field: command                  # optional: which input field to match (default depends on tool)
    reason: read-only listing       # optional: human-readable
    confidence: high                # optional: high | medium | low (informational)

  - tool: Read
  # No pattern means "all calls of this tool"

deny:
  - tool: Bash
    pattern: '\brm\s+-rf\s+/(?!tmp/)'  # rm -rf / except /tmp/...
    reason: destructive root-level rm

  - tool: Bash
    pattern: '\bgit\s+push\b.*--force'
    reason: force push forbidden

# Optional: short-circuit to agent without trying allow/deny
to_agent:
  - tool: Edit
    pattern: 'backend/internal/config/'
    reason: deployment-critical, must be agent-decided
```

## Field descriptions

### `version` (required)

Schema version. Currently `1`. Mismatch → arbiter logs warning and
attempts best-effort parse.

### `default` (optional, default `agent`)

What happens when no rule in `allow`, `deny`, or `to_agent` matches.

- `agent` — fall through to slow-path agent (recommended)
- `approve` — auto-approve (lax mode; for sandbox projects)
- `deny` — auto-deny (paranoid mode; usually too noisy)

### `allow` / `deny` / `to_agent`

Three lists of rules, evaluated in order: `deny` first, then `allow`,
then `to_agent`. (Rationale: a `deny` should always beat an `allow`. A
`to_agent` overrides both — useful when you want to force LLM judgment
even if a regex would otherwise match.)

Within each list, top-to-bottom; first match wins.

#### Rule fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `tool` | string | yes | Claude Code tool name: `Bash`, `Read`, `Edit`, `Write`, `Grep`, `Glob`, `Task`, etc. Use `"*"` for any tool |
| `pattern` | string | no | Go-flavor regex. If absent, matches all calls of `tool` |
| `field` | string | no | Which input field to match against (see Field selectors below) |
| `reason` | string | no | Logged with the decision; shown to user / Claude |
| `confidence` | enum | no | Informational only; future use for explainability |

### Field selectors

Different Claude Code tools have different input shapes. The default
field is sensible per tool:

| Tool | Default field | Example match target |
|---|---|---|
| `Bash` | `command` | `ls -la` |
| `Read` | `file_path` | `/etc/passwd` |
| `Edit` / `Write` | `file_path` | `backend/.env` |
| `Grep` | `pattern` + `path` | combined |
| `Glob` | `pattern` | `**/*.go` |
| `Task` | `description` | the task description text |

Override with `field: <name>` to match a different field. Example:

```yaml
deny:
  - tool: Edit
    field: new_string
    pattern: 'eval\('
    reason: no eval() in source
```

Special field `"raw"` matches the JSON-encoded entire input — useful for
multi-field rules.

## Evaluation semantics

```
on tool call:
    for rule in deny:
        if matches(rule, tool_call): return DENY(rule.reason)
    for rule in allow:
        if matches(rule, tool_call): return APPROVE(rule.reason)
    for rule in to_agent:
        if matches(rule, tool_call): return AGENT(rule.reason)
    return DEFAULT  # per `default:` field
```

`matches(rule, call)`:
1. `rule.tool == "*"` OR `rule.tool == call.tool_name`
2. AND if `rule.pattern` set: `regexp.Match(rule.pattern, field_value)`

## Performance

Compiled regexes cached by mtime. Steady-state evaluation is one map
lookup per rule until first match. ~1µs per rule, irrelevant in practice.

policy.yaml that doesn't match → adds ~1ms before falling through to
agent. Negligible.

## Anti-patterns

| Don't | Why |
|---|---|
| Try to encode `rules.md` semantics as regex | Regex can't reason about file content / context. Use rules.md for nuance |
| Put secrets in `reason` strings | Reasons are logged + may be shown to Claude |
| Use `default: deny` without thorough `allow` list | You'll deny everything, including read-only ops |
| Maintain duplicate rules in policy.yaml and rules.md | Pick one source of truth per concern |

## Minimal example (default-allow sandbox)

```yaml
version: 1
default: approve

deny:
  - tool: Bash
    pattern: '\brm\s+-rf\s+/'
    reason: catastrophic delete
```

## Strict example (default-deny, explicit allowlist)

```yaml
version: 1
default: agent      # agent decides anything not in allow/deny

deny:
  - tool: Bash
    pattern: '\brm\s+-rf\b(?!.*/tmp/)'
  - tool: Edit
    field: file_path
    pattern: '(\.env(\.|$)|\.pem$|\.key$|credentials)'
  - tool: Bash
    pattern: 'curl.*\|\s*(bash|sh)'

allow:
  - tool: Read
  - tool: Glob
  - tool: Grep
  - tool: Bash
    pattern: '^(ls|cat|head|tail|wc|grep|rg|fd|git\s+(status|log|diff|branch))\b'
```

See [examples/policy.yaml](../examples/policy.yaml) for an annotated
example.

## Versioning + migration

When the schema changes (new fields, breaking semantics), `version` will
bump and the arbiter will support both versions for at least 6 months.
Migration tool: `arbiter policy migrate <file>`.
