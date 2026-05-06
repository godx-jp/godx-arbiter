# Architecture

## TL;DR

godx-arbiter sits between any agentic CLI (Claude Code, Codex, Gemini,
Antigravity, …) and your filesystem / shell. The CLI calls it via hooks
(when supported) or via a local LLM proxy (universal fallback). Arbiter
decides whether to approve, deny, or escalate — using project-specific
rules written in plain Markdown — and may re-route the task to a
cheaper / better model.

```
┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│ Claude Code  │  │ Codex CLI    │  │ Gemini CLI   │  │ Antigravity  │
│  hook+MCP    │  │  proxy       │  │  proxy       │  │  proxy       │
└──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘
       │                 │                 │                 │
       └─────────────────┴────────┬────────┴─────────────────┘
                                  │
                                  ▼
                       ┌──────────────────────┐
                       │  ADAPTER LAYER       │
                       │  normalize event →   │
                       │  CanonicalEvent      │
                       └──────────┬───────────┘
                                  │
                                  ▼
                       ┌──────────────────────┐
                       │  ARBITER CORE        │
                       │  - load rules.md     │
                       │  - fast-path policy  │
                       │  - LLM agent         │
                       │  - model routing     │
                       │  - escalation        │
                       └──┬───────────────┬───┘
                          │               │
                  ┌───────┘               └────────┐
                  ▼                                ▼
          arbiter agent                    notify dispatcher
          (Anthropic Go SDK)                ├── telegram
          + MCP tools                       ├── desktop
                                            └── webhook
                          ▲
                          │
                  <project>/.arbiter/
                  ├── rules.md      (LLM reads)
                  └── policy.yaml   (regex fast-path)
```

## Two integration modes (Mode A: hook · Mode B: proxy)

The arbiter receives events via two complementary channels. See
[MULTI_CLI.md](MULTI_CLI.md) for details.

- **Hook mode** — for CLIs with native hook support (Claude Code).
  Lower latency for tool-call gating; richer event metadata.
- **Proxy mode** — universal. The arbiter runs as a local LLM proxy
  (default port 7777) speaking OpenAI / Anthropic / Gemini compatible
  APIs. Any CLI configured to use this endpoint flows through arbiter
  for every model invocation, every tool-use, every token.

The same `internal/decide` core handles canonicalized events from
either mode. Adapters (`internal/adapter/<cli>/`) translate between
each CLI's native format and the canonical event.

## The four layers

### Layer 1 — Core (the Go binary)

Single static binary, distributed via npm wrapper or direct download.
Subcommands:

| Command | Purpose |
|---|---|
| `arbiter hook pretool` | Receives `PreToolUse` payload on stdin, returns decision on stdout |
| `arbiter hook notification` | Receives `Notification` payload, dispatches to notify channels |
| `arbiter hook stop` | Receives `Stop` payload, sends session-end notification |
| `arbiter hook posttool` | (optional) Logs outcomes for `lookup_history` tool |
| `arbiter init` | Scaffolds `~/.claude/settings.json` hooks + `.arbiter/rules.md` template in cwd |
| `arbiter doctor` | Verifies install, env vars, Telegram connectivity |
| `arbiter mcp` | Runs MCP stdio server (used by Claude sessions wanting decision-support tools) |
| `arbiter proxy` | Runs the LLM proxy server for non-Claude CLIs (port 7777) |
| `arbiter usage` | Token + cost reporting per session / day / project |
| `arbiter explain <session-id> <event-id>` | Replay a past decision with full rationale |

### Layer 2 — Project config (per-project)

Each project that wants arbiter coordination has `.arbiter/`:

```
<project>/.arbiter/
├── rules.md       # Free-form Markdown rules — LLM reads + interprets
├── policy.yaml    # (optional) Regex fast-path — bypasses LLM
└── skills/        # (optional) Project-specific skill MD files
```

The arbiter detects the project by walking up from `cwd` looking for
`.arbiter/`. If absent, falls back to `~/.config/godx-arbiter/default-rules.md`.

### Layer 3 — Decision engine

Two paths:

**Fast-path (regex):** `policy.yaml` patterns checked first. If a tool call
matches `allow:` or `deny:`, decision is returned immediately. Zero LLM
cost, sub-millisecond latency. Use for the obvious cases (`ls`, `cat`, etc.).

**Slow-path (agent):** No fast-path match → spawn LLM sub-agent with:
- System prompt = `rules.md` + selected skill MDs + action JSON
- Tools = arbiter MCP toolset (analyze_risk, check_rule, lookup_history,
  read_file, escalate_to_user)
- Model = Claude Haiku by default (cheap, fast); upgradable per-rule

Agent loops: think → call tools → think → final decision (approve / deny / ask).

### Layer 4 — Notification

Pluggable channels for `escalate_to_user` and session-end events:

- **Telegram** — primary, remote-friendly, works on mobile
- **Desktop** — `notify-send` (Linux), `osascript` (macOS), Windows toast
- **Webhook** — HTTP POST for custom integrations (Slack, Discord, internal)
- **Log only** — write to `~/.local/share/godx-arbiter/events.log`

Channel selection per project (set in `rules.md` Notification section).
Quiet hours, deduplication, and timeout fallback all configurable.

## Components in Go

```
godx-arbiter/
├── cmd/
│   ├── arbiter/             # main CLI binary
│   │   └── main.go
│   └── arbiter-mcp/         # (alternative) standalone MCP binary
│       └── main.go
├── internal/
│   ├── config/              # load rules.md, policy.yaml, env
│   ├── decide/              # core: orchestrate fast-path → agent → return
│   ├── policy/              # fast-path regex evaluator
│   ├── agent/               # Anthropic Go SDK wrapper, tool loop
│   ├── tools/               # decision-support tools (also MCP-exposed)
│   │   ├── analyze_risk.go
│   │   ├── check_rule.go
│   │   ├── lookup_history.go
│   │   ├── read_file.go
│   │   ├── escalate_to_user.go
│   │   └── delegate_to.go   # cross-CLI handoff
│   ├── adapter/             # per-CLI translators (hook + proxy)
│   │   ├── adapter.go       # interface
│   │   ├── claudecode/      # hook + MCP
│   │   ├── codex/           # proxy + partial hook
│   │   ├── gemini/          # proxy + partial hook
│   │   └── antigravity/     # proxy
│   ├── proxy/               # LLM proxy server (Mode B)
│   │   ├── server.go        # http server on :7777
│   │   ├── translate/       # cross-provider format translation
│   │   │   ├── anthropic_openai.go
│   │   │   ├── anthropic_gemini.go
│   │   │   └── openai_gemini.go
│   │   ├── classify/        # task classification (heuristic + LLM)
│   │   ├── route/           # model routing engine
│   │   └── budget/          # token + cost tracking
│   ├── notify/              # pluggable: telegram, desktop, webhook
│   ├── mcp/                 # MCP server impl wrapping internal/tools
│   ├── hookio/              # parse Claude Code hook stdin/stdout JSON
│   ├── projectfind/         # walk up from cwd to find .arbiter/
│   ├── eventlog/            # append-only log for replay + lookup_history
│   └── usage/               # token + cost ledger (used by budget)
├── npm/                     # npm wrapper package
│   ├── package.json
│   └── install.js           # postinstall: download platform binary
├── examples/
├── docs/
├── go.mod
├── go.sum
└── Makefile                 # cross-compile linux/darwin/windows × amd64/arm64
```

## Data flow — typical PreToolUse

```
1. Claude Code wants Bash("rm -rf node_modules") in cwd=/home/u/famgia/admin
2. Claude Code reads ~/.claude/settings.json hook config
3. Claude Code spawns: arbiter hook pretool
4. Stdin to arbiter:
   {
     "session_id": "abc123",
     "tool_name": "Bash",
     "tool_input": {"command": "rm -rf node_modules", "description": "..."},
     "cwd": "/home/u/famgia/admin"
   }
5. arbiter:
   a. projectfind walks up cwd → finds /home/u/famgia/admin/.arbiter
   b. config loads rules.md + policy.yaml
   c. policy fast-path: pattern "^rm -rf " matches deny rule → return deny
   d. (or) no match → agent.Decide(action, rules, tools) called
   e. agent thinks, calls tools, returns {decision: "deny", reason: "..."}
6. arbiter writes to stdout:
   {
     "decision": "block",
     "reason": "rules.md says rm -rf outside /tmp is denied"
   }
7. Claude Code receives stdout, blocks the tool call, shows reason to Claude
```

## Why Go for the core

- **Single static binary** — no runtime, no Node.js, no Python deps in
  user environment.
- **Source not shipped** — compiled binary is the artifact; npm wrapper
  doesn't include Go source.
- **Cross-compile easy** — `GOOS=linux GOARCH=arm64 go build` from any
  dev machine.
- **Stack consistency** — famgia-admin backend is Go; same idioms.
- **Cold-start fast** — hook latency matters (it's on every tool call);
  Go starts in ~10ms vs Node ~80ms vs Python ~150ms.

Tradeoff: Anthropic's official Agent SDK is Python/TS. We use the raw
Anthropic Go SDK and implement the tool loop ourselves — small price for
the deployment benefits.

## Why npm for installation

- `npm i -g godx-arbiter` is muscle memory for most developers.
- npm package is tiny: ~5 KB postinstall script that downloads the
  appropriate prebuilt Go binary from GitHub Releases.
- This pattern is proven (esbuild, swc, biome, turbo all do this).
- Direct `curl ... | bash` install is also supported for non-Node users.

## What ships, what stays

| Artifact | Public? | Distribution |
|---|---|---|
| Go source | Private (initially) | Internal repo only |
| Compiled binary | Public | GitHub Releases |
| npm wrapper | Public | npm registry |
| Docs | Public | this repo (or split docs site) |
| Project's `rules.md` | Per-project decision | In project repo (likely) |

## Extension points

- **New notification channel**: implement `internal/notify/Channel`
  interface, register in `internal/notify/registry.go`.
- **New decision-support tool**: drop a file in `internal/tools/`,
  register in `internal/tools/registry.go`. Becomes available to both the
  arbiter agent and external MCP consumers automatically.
- **New skill**: drop a Markdown file in `~/.config/godx-arbiter/skills/`
  or `<project>/.arbiter/skills/`. Referenced by name in `rules.md`.
- **Custom agent model / provider**: `internal/agent` interface allows
  swap to OpenAI, Gemini, or local model — though Claude is the default
  and best-tuned for `rules.md` parsing.

## Out of scope (explicitly)

- Replacing `claude` itself — we're a coordinator, not a fork.
- Multi-user / team policy server — single-user local tool for now.
- Audit log compliance / SOC2 — eventlog is for `lookup_history`, not
  legal audit.
- Sandboxing tool execution — Claude Code's permission system + arbiter
  decisions are the boundary; we don't run tools ourselves.

## Glossary

| Term | Meaning |
|---|---|
| Hook | Claude Code mechanism: shell command invoked on lifecycle events |
| PreToolUse | Hook event fired before Claude executes a tool |
| Fast-path | Regex-based decision in policy.yaml (no LLM) |
| Slow-path | LLM agent decision using rules.md |
| Escalation | Sending a question to the user via notify channel |
| Skill | Reusable best-practice MD chunk loaded into agent context |
| MCP | Model Context Protocol — how tools are exposed to LLMs |
