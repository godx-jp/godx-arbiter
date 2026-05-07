# Multi-CLI support

godx-arbiter coordinates more than just Claude Code. The same arbiter
binary, the same `.arbiter/rules.md`, mediates work across:

- **Claude Code** (Anthropic)
- **Codex CLI** (OpenAI)
- **Gemini CLI** (Google)
- **Antigravity** (Google's agentic coding tool)
- (extensible — adapters are pluggable)

This doc explains how, given that these tools have wildly different
integration surfaces.

## The problem

Claude Code has a rich hook system (PreToolUse / Notification / Stop) and
MCP support. We use those in the [base architecture](ARCHITECTURE.md).

Other CLIs:
- **Codex CLI**: tool-calling via OpenAI Responses API; configurable but
  hook surface is shallow. Some sandbox controls.
- **Gemini CLI**: tool-calling via Gemini Function Calling. Hooks on
  some events; not as rich as Claude Code's.
- **Antigravity**: agent loop with its own task abstraction; integration
  surface still evolving.

A pure-hook approach would only work for Claude Code. For universal
coverage we add a **second integration mode**: LLM proxy.

## Two integration modes

### Mode A — Hook adapter (preferred where supported)

```
┌──────────────────┐     hook event JSON     ┌─────────────┐
│  Claude Code     │  ──────────────────────►│  arbiter    │
│  Codex (partial) │  ◄────  decision  ──────│  core       │
│  Gemini (partial)│                         └─────────────┘
└──────────────────┘
```

For each supported CLI, an **adapter** translates that CLI's native
hook/event format into arbiter's canonical event schema, and translates
arbiter's decision back to the CLI's expected response shape.

Adapters live in `internal/adapter/<cli>/`:

```
internal/adapter/
├── claudecode/      # Claude Code hooks (native — minimal translation)
├── codex/           # Codex CLI events (where supported)
├── gemini/          # Gemini CLI events
├── antigravity/     # Antigravity events
└── adapter.go       # interface contract
```

Adapter interface:

```go
type Adapter interface {
    Name() string                                       // "claude-code", "codex", ...
    ParseEvent(raw []byte) (CanonicalEvent, error)
    EncodeDecision(d Decision) ([]byte, error)
    Capabilities() Capabilities                         // which hooks the CLI supports
}
```

Canonical event:

```go
type CanonicalEvent struct {
    SessionID  string
    CLI        string                  // "claude-code" | "codex" | ...
    Cwd        string
    Phase      Phase                   // PreTool | PostTool | Notification | Stop
    Tool       Tool                    // normalized: name, input, etc.
    ModelHint  string                  // model the CLI is using
    Metadata   map[string]any          // CLI-specific extras
}
```

So `internal/decide` works on `CanonicalEvent` — it doesn't care which
CLI sent the event. Adapters are the only CLI-aware code.

### Mode B — LLM proxy (universal fallback)

For CLIs with weak/no hook support, arbiter runs as a local LLM proxy
exposing OpenAI-compatible / Anthropic-compatible / Gemini-compatible
endpoints. The CLI is configured to point at arbiter:

```
~/.codexrc  →  api_base: http://localhost:7777/v1
~/.geminirc →  api_base: http://localhost:7777/v1beta
```

Flow:

```
┌──────────┐  POST /v1/responses (OpenAI)    ┌──────────────────────┐
│  Codex   │ ───────────────────────────────►│  arbiter proxy       │
│  CLI     │                                  │  :7777               │
│          │ ◄────  rewritten response  ─────│                      │
└──────────┘                                  └──────────┬───────────┘
                                                         │
                              ┌──────────────────────────┴────────┐
                              │ 1. parse request                  │
                              │ 2. apply rules.md routing         │
                              │    (which model? which provider?) │
                              │ 3. forward to actual API          │
                              │ 4. intercept tool_use in response │
                              │ 5. apply per-tool-call gate       │
                              │    (same logic as hook mode)      │
                              │ 6. return response to CLI         │
                              │ 7. log tokens / cost / decision   │
                              └───────────────────────────────────┘
                                                         │
                              ┌──────────────────────────┴────────┐
                              ▼                ▼                  ▼
                       Anthropic API     OpenAI API         Gemini API
```

Key benefits of proxy mode:
- Works with any CLI that speaks one of the major LLM APIs.
- Catches every tool call before it reaches the CLI — universal gate.
- Catches every model invocation — enables model routing + token
  accounting that hook mode can't see (hooks fire on tool call, not on
  every model turn).

Cost: extra latency per turn (~5-15ms local proxy + provider call). Same
as direct in practice.

### Hybrid mode (recommended for Claude Code + others)

Run both:
- Claude Code uses **hook mode** (richer, lower-latency for tool gates)
- Codex / Gemini / Antigravity use **proxy mode**
- All converge on the same `internal/decide` core
- All read the same `<project>/.arbiter/rules.md`

Single `arbiter` binary. Two ports of entry. One rule set.

## Per-CLI capabilities matrix

| Capability | Claude Code | Codex CLI | Gemini CLI | Antigravity |
|---|---|---|---|---|
| Hook on tool call | ✅ native | ⚠️ partial | ⚠️ partial | ❓ TBD |
| Hook on session start/stop | ✅ | ⚠️ | ⚠️ | ❓ |
| MCP support | ✅ | ⚠️ in progress | ⚠️ | ❓ |
| Proxy mode (universal) | ✅ | ✅ | ✅ | ✅ |
| Notification hook | ✅ | ❌ | ❌ | ❌ |
| Recommended mode | hook + MCP | proxy + partial hook | proxy + partial hook | proxy |

(This matrix is best-effort as of 2026-05; updated as adapters land.)

## Per-CLI configuration

Each adapter has a config block in `~/.config/godx-arbiter/config.yaml`:

```yaml
clis:
  claude-code:
    mode: hook
    hooks:
      pre_tool: true
      notification: true
      stop: true
    mcp_register: true

  codex:
    mode: proxy
    proxy_endpoint: http://localhost:7777/v1
    api_key_env: ARBITER_OPENAI_KEY    # arbiter holds the real key; CLI gets a dummy

  gemini:
    mode: proxy
    proxy_endpoint: http://localhost:7777/v1beta
    api_key_env: ARBITER_GEMINI_KEY

  antigravity:
    mode: proxy
    proxy_endpoint: http://localhost:7777/v1     # speaks OpenAI-compatible
```

`arbiter init` walks through each detected CLI and configures it.

## Cross-CLI coordination

With all CLIs funneled through arbiter, cross-tool orchestration becomes
viable. Two patterns:

### Pattern 1 — `delegate_to` MCP tool

A running session can call the arbiter MCP tool `delegate_to` to spawn
another CLI for a sub-task:

```
[Claude Code session] thinks: "this needs deep code generation, Codex is
better for that here"
  → calls mcp__godx-arbiter__delegate_to(
        cli="codex",
        task="implement the migration script described in <plan>",
        context={...},
        budget_tokens=20000
    )
  → arbiter spawns codex non-interactively, captures output
  → returns synthesized result to Claude Code
```

This is **explicit handoff**: the calling CLI decides to delegate.

### Pattern 2 — Implicit routing in proxy mode

In proxy mode, `rules.md` can route entire task classes to a different
CLI/model without the CLI knowing:

```markdown
## Model + tool routing

- Tasks tagged `code-generation` (>500 LOC change): route to Codex via
  background invocation; return summarized result
- Tasks tagged `summarization`: use Gemini Flash (cheapest for that task)
- Tasks tagged `planning`: keep on the calling CLI's primary model
```

The arbiter reads task intent from the first user message + tool-call
patterns, classifies, and routes. The user's CLI sees a coherent reply;
underneath, work happened on a different model/CLI.

## Streaming + tool gating in proxy mode

The proxy gates tool calls in **both** streaming and non-streaming
responses. For Anthropic and OpenAI, we parse the SSE chunk stream
directly and rewrite tool_use events when the policy denies them.
Gemini's streaming format aggregates function calls server-side;
non-streaming gating already covers the common case there.

| Provider | Non-streaming | Streaming (`text/event-stream`) |
|---|---|---|
| Anthropic | ✅ rewrites `tool_use` → text refusal block | ✅ buffers `input_json_delta`, rewrites on deny |
| OpenAI | ✅ rewrites `tool_calls[*].function.arguments` | ✅ buffers argument deltas, rewrites on deny |
| Gemini | ✅ via `functionCall` parts | ⚠ passthrough (function calls aren't chunk-streamed in practice) |

When a deny rewrite happens, response headers gain
`X-Arbiter-Refused: <tool>` so calling tooling can detect it without
parsing the body.

See `internal/proxy/sse.go` (Anthropic), `internal/proxy/sse_openai.go`
(OpenAI), and `internal/proxy/wire.go` (non-streaming + token
accounting) for the implementations.

## Capability snapshot vs implementation

The matrix above tracks what each CLI's surface theoretically supports;
the table below tracks what arbiter actually wires today:

| Adapter | Status | Notes |
|---|---|---|
| `internal/adapter/claudecode` | ✅ full reference | All hook events (PreTool / PostTool / Notification / Stop / UserPrompt) + MCP + proxy |
| `internal/adapter/codex` | ✅ thin | Generic event parser; uses Claude-Code-shaped output |
| `internal/adapter/gemini` | ✅ thin | Generic event parser; outputs Gemini's `{decision: allow|block|ask}` shape |
| `internal/adapter/antigravity` | ✅ thin | Best-effort, expected to evolve as the CLI's API stabilizes |

Adapters are pluggable — see [CONTRIBUTING.md](../CONTRIBUTING.md#adding-a-new-cli-adapter).

## Open questions

- Antigravity API surface: still flux. Adapter is best-effort until
  stabilized.
- Auth: holding multiple provider API keys in arbiter is sensitive —
  arbiter uses OS keychain integration (`go-keyring`) by default. See
  [CONFIG.md](CONFIG.md#environment-variables) and
  [`arbiter auth`](CLI.md#arbiter-auth).
- Proxy port collision with other local LLM tools (Ollama on 11434,
  LM Studio, etc.): default to 7777, configurable via
  `arbiter proxy --addr` or `proxy.addr` in `config.yaml`.

See [MODEL_ROUTING.md](MODEL_ROUTING.md) for the routing rules and token
optimization strategy.
