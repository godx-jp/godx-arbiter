# Model routing & token optimization

The arbiter doesn't just decide approve/deny. It also decides **which
model handles which task**, optimizing for cost (tokens) and quality.

## Why route?

Modern coding sessions hit many task types in one flow:
- Reading + understanding code (mid-effort)
- Writing simple boilerplate (low-effort)
- Hard architectural reasoning (high-effort)
- Summarizing logs / outputs (low-effort)
- Final code review (high-effort)

Using a top-tier model (Opus / GPT-5 / Gemini Pro) for everything is
expensive. Using a small model for everything misses on hard tasks. The
right answer is **task-aware routing**.

The arbiter learns task class from:
- Tool the CLI is about to call
- The user prompt + recent context
- The file/path being touched
- `rules.md` heuristics

…and rewrites the model parameter on the way to the provider.

## Where routing happens

Only in **proxy mode** (Mode B in [MULTI_CLI.md](MULTI_CLI.md)). Hook
mode doesn't see model invocations — only tool calls. That's a
fundamental limit: we can't reroute a model the CLI invokes directly
unless we sit on the API path.

(Future: some CLIs may expose model-selection hooks. We'll adopt them
when they ship.)

## Routing config — per project

In `<project>/.arbiter/rules.md`, add a `## Model routing` section:

```markdown
## Model routing

Default models per CLI when no rule below matches:
- Claude Code: claude-sonnet-4-6
- Codex: gpt-5
- Gemini: gemini-2.5-pro

Rules (top to bottom; first match wins):

- task: read-only-summarization
  model: claude-haiku-4-5-20251001
  reason: cheap, fast enough for log/diff summaries
  match:
    - tool: Read|Glob|Grep followed by short reasoning
    - prompt contains: "summarize", "tldr", "what does this do"

- task: simple-edit
  model: claude-haiku-4-5-20251001
  match:
    - tool: Edit
    - file size < 500 lines
    - change type: comment, formatting, rename

- task: hard-reasoning
  model: claude-opus-4-7
  match:
    - prompt contains: "architecture", "design", "tradeoff"
    - OR: more than 5 files implicated

- task: code-generation-large
  model: codex-via-delegate     # cross-CLI handoff
  match:
    - new file > 200 lines
    - or refactor across > 10 files

- task: cheap-fallback
  model: gemini-2.5-flash
  match:
    - any when tokens-this-session > 80% of budget
```

## Token / cost budgets

```markdown
## Budget

- Per-session soft limit: 200_000 tokens
- Per-session hard limit: 500_000 tokens
- Per-day hard limit: $5.00

When soft limit hit: arbiter prefers cheap models, escalates "are you
sure?" before switching to expensive models.

When hard limit hit: arbiter denies further model calls and notifies
the user via Telegram.
```

The arbiter tracks usage in
`~/.local/share/godx-arbiter/usage.jsonl`:

```json
{"ts":"2026-05-05T10:11:22Z","session":"abc","cli":"claude-code","model":"claude-sonnet-4-6","input_tokens":12340,"output_tokens":890,"cost_usd":0.041}
```

`arbiter usage` summarizes:

```
$ arbiter usage --today
session abc — claude-code — sonnet — 84.5k tok — $0.42
session def — codex      — gpt-5  — 22.1k tok — $0.28
session ghi — gemini     — pro    — 55.8k tok — $0.31

Total today: 162.4k tok, $1.01 (limit $5.00 — 20% used)
```

## Routing decision pipeline

When a CLI calls a model via the proxy:

```
1. Receive request → parse model, messages, tool definitions
2. Load <project>/.arbiter/rules.md routing section
3. Classify task: small heuristic + (optionally) tiny LLM call
   - input: last user message + recent tool calls + file context
   - output: task tag (e.g., "simple-edit")
4. Check budget: if hard limit, deny + notify
   If soft limit, prefer cheap path
5. Pick model: first matching rule's model
6. Translate format if cross-provider:
   - Anthropic ↔ OpenAI: tool schema, message roles, system prompts
   - Gemini ↔ OpenAI: function declarations, contents
7. Forward to actual provider
8. Receive response → translate back to original format
9. Log tokens + cost
10. Return to CLI
```

## Cross-provider format translation

The arbiter ships translators for:

| From → To | Status |
|---|---|
| Anthropic ↔ OpenAI | Step 11 of roadmap |
| Anthropic ↔ Gemini | Step 11 |
| OpenAI ↔ Gemini | Step 11 |

Translators live in `internal/proxy/translate/`. Care points:
- Tool definitions: parameter schemas, naming
- System prompt: Anthropic = top-level, OpenAI = first message, Gemini =
  systemInstruction
- Tool-use blocks vs function-call: nested vs top-level
- Streaming: SSE chunks differ in shape

## Task classification

Two classifier modes:

### Heuristic (fast, free)

Regex/keyword on prompt + tool patterns:

```yaml
classifiers:
  - tag: read-only-summarization
    when:
      tool_name_in: [Read, Glob, Grep]
      OR:
        prompt_contains_any: ["summarize", "tldr", "explain briefly"]

  - tag: simple-edit
    when:
      tool_name: Edit
      and:
        - file_size_loc_lt: 500
        - change_class_in: [comment, format, rename]
```

### LLM-classifier (accurate, ~$0.0001 per call)

Use Haiku to classify. Cached aggressively (same prompt prefix → cached
classification).

```
[Haiku] Given the user message and tool history below, return one of:
read-only-summarization, simple-edit, hard-reasoning, code-generation-large, other

User message: "..."
Recent tools: [...]

Answer (one tag):
```

Hybrid: try heuristic first; if no rule matches and confidence < 0.6,
fall back to LLM classifier.

## Examples

### Token-heavy refactor

Claude Code session, user says "rewrite the auth middleware to use the
new session token API."

- Routing: matches `task: hard-reasoning` (>5 files implicated, prompt
  has "rewrite") → keep on Sonnet (default).
- During execution, Claude calls `Edit` 12 times. Each edit is
  classified `simple-edit` if change_class is mechanical → arbiter
  internally could route those to Haiku, but: edits in proxy mode happen
  inside a single Claude turn, so this requires a tool-level proxy
  intercept. Defer to v2.

### Cheap log summarization

User running Codex says "what changed in the last 50 commits?"

- Tool calls: Bash `git log --oneline -50`, then Codex tries to
  summarize.
- Routing: `task: read-only-summarization` matches → arbiter rewrites
  model from `gpt-5` to `gemini-2.5-flash`.
- Codex sees a normal response; cost dropped 30x.

### Budget exhaustion

User has used 90% of daily budget. Asks Claude to "redesign the schema".

- Routing: `task: hard-reasoning` would normally pick Opus.
- Budget check: soft limit exceeded → prefer cheap → downgrade to
  Sonnet.
- arbiter prepends a system note: "Budget 90% used; using Sonnet
  instead of Opus. To override, reply BUDGET_OVERRIDE."

## Token-optimization tactics (built-in)

Beyond model routing, the proxy applies:

- **Prompt cache**: when forwarding to Anthropic, set `cache_control`
  on long stable prefixes (system prompt, rules.md context, recent
  conversation up to last user turn). Saves ~60% on repeat turns.
- **Tool-result truncation**: if a tool result is > N KB and likely
  irrelevant in full (e.g., big `find` output), summarize it before
  feeding to the model.
- **Skip-think-when-trivial**: if heuristic says task is trivial,
  disable extended thinking.
- **Context window trimming**: strip old turns when the conversation
  exceeds 60% of the model's window.

Each tactic is a toggle in `rules.md`:

```markdown
## Optimization

- prompt_cache: aggressive
- tool_result_truncation: enabled, threshold 8 KB
- skip_thinking: trivial-tasks
- context_trim: 60%
```

## Anti-patterns

| Don't | Why |
|---|---|
| Route to a model the project hasn't tested with | Quality regressions surprise users; surface routing in logs |
| Make routing rules opaque ("smart routing!") | Users need to predict cost; route reasons must be in `arbiter explain` |
| Hide cost overruns | Always notify on budget thresholds |
| Translate formats lossy-ly without warning | If a tool schema can't translate, fail with a clear error rather than silently dropping fields |

## Related

- [MULTI_CLI.md](MULTI_CLI.md) — proxy mode mechanics
- [MCP_TOOLS.md](MCP_TOOLS.md) — tools the agent can use mid-decision
- [DECISIONS.md](DECISIONS.md) — ADR-006 covers proxy + routing rationale
