# Slow-path agent internals

The slow-path is the LLM agent that decides when `policy.yaml` can't.
This doc covers what's actually sent to the model, how the tool loop
runs, and how the final decision is parsed.

For the higher-level pipeline view, see
[DECISION_FLOW.md](DECISION_FLOW.md).

## When the agent runs

```
policy.yaml fast-path
  ↳ Outcome.Deny  → fast-path deny, no agent
  ↳ Outcome.Allow → fast-path allow, no agent
  ↳ Outcome.Agent → slow-path agent
```

`Outcome.Agent` triggers when:

- A `to_agent` rule matches (e.g. `\bsudo\b`)
- No rule matches and `default: agent` (the documented default)
- No `policy.yaml` exists at all

## Construction

```go
cfg := agent.DefaultConfig(rules)            // pulls front matter knobs
cfg.Tools      = tools.DefaultRegistry()     // 8 decision-support tools
cfg.SkillTexts = skills.Resolve(...)         // @include skill:* expanded
cfg.RulesBody  = rules.Body                  // markdown body verbatim

llm, _ := agent.NewAnthropicLLM()            // resolves API key via auth
decision := agent.New(llm).Decide(ctx, cfg, action)
```

`DefaultConfig` reads the `rules.md` front matter:

| Front-matter key | Default | Effect |
|---|---|---|
| `agent_model` | `claude-haiku-4-5-20251001` | Model id for the decision call. |
| `timeout_seconds` | 30 | Hard ceiling on the whole loop. |
| `max_agent_iterations` | 10 | Tool-use round-trips before fallback. |
| `on_timeout` | `deny` | Outcome when the timeout fires. |
| `on_error` | `approve` | Outcome on internal errors (ADR-005). |

## System prompt structure

```
You are godx-arbiter, a decision agent for an AI coding CLI.
Your job: decide whether the proposed tool call should be approved,
denied, or escalated to the human user. Use the available tools to
gather context (analyze_risk, check_rule, lookup_history, read_file)
before deciding. Be concise. End with exactly one line:
  ARBITER_DECISION: approve
  ARBITER_DECISION: deny — <one-sentence reason shown to the calling agent>
  ARBITER_DECISION: ask — <question for the human>

--- PROJECT rules.md ---
<verbatim rules.md body>
--- end rules.md ---

--- SKILL ---
# Skill: <name>
<skill body>
--- end skill ---
... (repeat per skill)

--- ACTION UNDER REVIEW ---
tool:    Bash
cwd:     /home/u/famgia/admin
project: /home/u/famgia/admin
session: abc-123
input:   {"command":"git push --force origin staging"}
--- end action ---
```

The whole system block is sent with `cache_control: ephemeral` so
subsequent decisions in the same 5-minute window hit the prompt cache.
Ballpark: ~60% of system tokens become free on cache hit (per
Anthropic's cache pricing).

## Tool loop

```go
for iter := 0; iter < cfg.MaxIterations; iter++ {
    reply, err := llm.Send(ctx, system, turns, tools, model, maxTokens)
    if err != nil { /* fallback per on_timeout / on_error */ }

    turns = append(turns, assistantTurn(reply.Blocks))

    var toolUses []Block
    for _, b := range reply.Blocks {
        if b.Type == BlockToolUse { toolUses = append(toolUses, b) }
    }
    if len(toolUses) == 0 {
        // Final answer — parse decision out of the assistant text.
        return parseDecision(textBlocks(reply.Blocks))
    }

    var results []Block
    for _, tu := range toolUses {
        out, err := registry.Execute(ctx, tu.ToolName, tu.ToolInput)
        results = append(results, toolResult(tu.ToolUseID, out, err))
    }
    turns = append(turns, userTurn(results))
}
return fallback(cfg.OnTimeout, "agent exhausted max iterations")
```

The inner loop:
- Sends the running conversation back to the model
- If the model answers without tool calls → parse + return
- Otherwise execute each tool, append the results as a single user
  turn, loop again

## Available tools

The 8 tools in [`internal/tools.DefaultRegistry()`](MCP_TOOLS.md):

| Tool | What the agent gets |
|---|---|
| `analyze_risk` | Score (0..1) + category + concerns + reversibility + blast_radius |
| `check_rule` | Section / keyword excerpt from `rules.md` |
| `lookup_history` | Recent decisions matching tool/pattern/session |
| `read_file` | Bounded UTF-8 file content (8 KiB cap) within project root |
| `list_recent_actions` | Recent decisions in the same session |
| `get_project_meta` | Branch, recent commits, languages, CLAUDE.md presence |
| `escalate_to_user` | Sends a notification + waits for reply (the only side-effect tool) |
| `delegate_to` | Runs another CLI non-interactively for a sub-task |

Tools are dispatched through the same registry that powers
[`arbiter mcp`](CLI.md#arbiter-mcp), so adding one lights it up
internally and externally at the same time.

## Decision parser

The agent must end its final assistant text with one of:

```
ARBITER_DECISION: approve
ARBITER_DECISION: deny — <reason>
ARBITER_DECISION: ask — <question>
```

The parser is tolerant: em-dash, hyphen, or colon as the separator;
case-insensitive on the outcome word; falls back to JSON
(`{"decision":"deny","reason":"..."}`) if the marker is missing but
the entire output is a single JSON object.

If neither shape parses, the agent's output is treated as **deny**
with reason "agent did not emit ARBITER_DECISION line; refusing".
This is intentionally strict — silent allow on a malformed model
response would defeat the whole purpose.

## Escalation flow

When the agent picks `ask`:

```
agent → escalate_to_user(question, options, channel?, timeout?)
       ↓
notify.Default.Dispatch (quiet-hours filter + dedup)
       ↓
first available channel.Ask(ctx, request)
       ↓ (reply | timeout)
```

- `reply.Reply == "approve" | "allow"` → final decision: allow
- `reply.Reply == "deny" | "block"` → final decision: deny
- `reply.Timeout == true` → fall back per `rules.md` `on_timeout`

`docs/MCP_TOOLS.md#escalate_to_user` covers the message format.

## Fail-open / fail-closed

Per ADR-005 the documented default is `on_error: approve` (fail-open).
The reasoning: a buggy arbiter must not block all developer work.

Three real-world failure modes:

1. **No API key** → `agent unavailable` → `on_error` policy applies
2. **Network down / model API error** → retry once, then `on_error`
3. **Iteration cap hit** → `on_timeout` policy applies

For paranoid setups (`on_error: deny`, `on_timeout: deny`), the agent
defaults to refusal whenever it can't reach a confident verdict. Trade
availability for safety.

## Cost discipline

A typical decision (Haiku):

| | Input | Output | Cost |
|---|---|---|---|
| First call (cold cache) | ~5k tokens | ~500 tokens | ~$0.005 |
| Same session, 5min window (warm cache) | ~5k tokens (60% cached) | ~500 tokens | ~$0.002 |

The target in [`docs/MODEL_ROUTING.md`](MODEL_ROUTING.md) is
**< $0.001 per decision** averaged across a session — achievable when
prompt caching kicks in and most decisions stay on the fast-path.

For projects where decisions matter more than cost, override
`agent_model: claude-sonnet-4-6` (or `claude-opus-4-7`) in
`rules.md` front matter.

## Testing

The slow-path is fully testable without hitting the network — the LLM
interface is abstracted as `agent.LLM`, and `agent.MockLLM` accepts a
scripted reply slice. See
[`internal/agent/agent_test.go`](https://github.com/godx-team/godx-arbiter/blob/main/internal/agent/agent_test.go)
for the patterns:

- Direct approve / deny / ask
- Multi-turn tool-use loop
- Network error → fail-open / fail-closed
- Iteration cap → fallback
- JSON-decision fallback parser
- Malformed output → default deny

For full-stack tests against a live model, set
`ANTHROPIC_API_KEY` and run a hook against a project — the agent
trace lands in the eventlog (`arbiter explain --last -v`).

## Slow-path agent vs `arbiter run`

Two distinct invocation paths share the same gating fabric:

| | Slow-path agent | `arbiter run` |
|---|---|---|
| When it fires | Fast-path policy returns `OutcomeAgent` | User invokes the subcommand |
| What it spawns | Anthropic API directly via `anthropic-sdk-go` | The `claude` CLI via `--print --output-format stream-json` |
| Tools | `internal/tools.DefaultRegistry()` | Whatever Claude Code itself loads (settings.json, CLAUDE.md) |
| Hooks on tool use | Claude Code's hooks fire (i.e. arbiter gates itself) | Same — recursion fuse caps depth |
| Output | Internal — feeds the hook decision JSON | User-facing — streamed to terminal |

Both use the same eventlog with `Path: "slow-path"` vs `Path: "run"`
respectively. `arbiter explain --last -v` works on either.

See [RUN.md](RUN.md) for the run-mode specifics.
