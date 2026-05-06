# Roadmap

Build plan, broken into independently-shippable steps. Each step ends
with a working, testable artifact.

Status legend: ◯ todo · ◐ in progress · ● done

## Step 1 — Repo scaffold + CLI shim ●

**Goal**: `arbiter hook pretool` runs, parses stdin, returns
`{"decision":"approve"}`. No real logic. Establishes module structure.

Deliverables:
- ✅ `go.mod` — `github.com/godx-team/godx-arbiter`, Go 1.22
- ✅ `cmd/arbiter/main.go` — stdlib subcommand router (no cobra dep,
  for cold-start), `hook|init|doctor|mcp|proxy|usage|explain|version|help`
- ✅ `internal/hookio/` — `Input`, `Output`, `ReadInput`, `WriteApprove`,
  `WriteBlock`, `WriteWithMetadata`
- ✅ `Makefile` — `build`, `test`, `vet`, `fmt`, `tidy`, `cross-compile`,
  `smoke`
- ✅ `.github/workflows/ci.yml` — vet + test + build + smoke + cross-compile
- ✅ Unit tests: `internal/hookio/hookio_test.go` (8 cases)
- ✅ Fail-open behavior baked in (panics caught, errors → approve, per ADR-005)

Done criterion verified: smoke test outputs
`{"decision":"approve","reason":"step-1-stub: tool=Read session=smoke"}`
in < 10ms (below `time -p` resolution). Binary 1.7 MB stripped.

## Step 2 — Project detection + config loading ◯

**Goal**: arbiter finds `.arbiter/rules.md` + `policy.yaml` from cwd,
parses, caches.

Deliverables:
- `internal/projectfind/` — walk-up search
- `internal/config/rules.go` — Markdown + front matter parser
- `internal/config/policy.go` — YAML parser, schema validator
- mtime-based cache
- `arbiter doctor` reports detected project + parsed config

## Step 3 — Fast-path policy engine ◯

**Goal**: `policy.yaml` regex rules evaluated, decisions returned without
LLM.

Deliverables:
- `internal/policy/eval.go` — rule evaluation (deny → allow → to_agent → default)
- Per-tool field selectors
- Compiled regex cache
- Comprehensive unit tests with table-driven cases

Done when: a project with `policy.yaml` containing 5 deny + 5 allow rules
sees ~1ms p50 fast-path latency.

## Step 4 — Slow-path agent (Anthropic Go SDK + tool loop) ◯

**Goal**: When fast-path falls through, an LLM agent decides. No external
MCP yet — tools are internal.

Deliverables:
- `internal/agent/anthropic.go` — wraps `github.com/anthropics/anthropic-sdk-go`
- `internal/agent/loop.go` — tool-use loop, max-iterations bound, timeout
- `internal/tools/` — initial set: `analyze_risk`, `check_rule`,
  `lookup_history`, `read_file`
- `internal/tools/registry.go` — registry pattern for tool dispatch
- Decision parser (extract `ARBITER_DECISION:` from final text)
- Eventlog append on decision
- Integration tests with mock Anthropic responses

Done when: a tool call not matching `policy.yaml` triggers the agent,
which returns approve/deny within 30s with reasoning logged.

## Step 5 — Notification + escalation ◯

**Goal**: `escalate_to_user` tool wired up. Telegram + desktop channels
working.

Deliverables:
- `internal/notify/` — Channel interface
- `internal/notify/telegram.go` — Bot API client
- `internal/notify/desktop.go` — `notify-send` (Linux), `osascript`
  (macOS), `powershell` (Windows)
- `internal/notify/registry.go`
- Inline-keyboard reply on Telegram (Approve / Deny / Custom)
- Timeout + fallback policy
- `arbiter doctor --notify-test`

Done when: agent escalates → Telegram message arrives → user clicks
Approve → arbiter returns approve, all under 30s.

## Step 6 — MCP server ◯

**Goal**: `arbiter mcp` exposes the same tools to external Claude
sessions.

Deliverables:
- `internal/mcp/server.go` — stdio JSON-RPC MCP server
- Reuse `internal/tools/registry.go` (zero duplication)
- Schema generation from Go struct tags
- Sample registration snippet in INSTALL.md
- Conformance test against MCP Inspector

## Step 7 — Skills system + best-practice library ◯

**Goal**: Reusable skill MD files the agent can pull into context.

Deliverables:
- `~/.config/godx-arbiter/skills/` (global)
- `<project>/.arbiter/skills/` (project)
- Reference syntax in rules.md: `@include skill:review-before-merge`
- Initial library:
  - `review-before-merge.md` — pre-merge checklist
  - `test-before-deploy.md`
  - `safe-bash-allowlist.md` — common safe commands
  - `migration-discipline.md`
  - `secret-scanning.md`

## Step 8 — Distribution ◯

**Goal**: `npm i -g godx-arbiter` works on Linux / macOS / Windows.

Deliverables:
- `npm/package.json` + `npm/install.js`
- GitHub Releases workflow: cross-compile + checksum + upload
- `install.sh` for curl-pipe install
- Signed binaries (cosign or notarization on macOS)
- README quick-start verified

## Step 9 — `arbiter explain` + replay ◯

**Goal**: Past decisions are debuggable.

Deliverables:
- `arbiter explain <session-id>` — show timeline of decisions
- `arbiter explain --last` — most recent decision detail
- Output: action, fast-path eval, agent transcript, tools called,
  final decision, rules.md SHA at time of decision

## Step 10 — Docs site (optional, polish) ◯

**Goal**: docs.godx-arbiter.dev or similar. mdBook / Docusaurus.

Not required for v1.

## Step 11 — Adapter framework + proxy server skeleton ◯

**Goal**: `internal/adapter` interface in place; one adapter per
supported CLI (claudecode native; others stub). Proxy server runs on
:7777 and forwards 1:1 to upstream provider.

Deliverables:
- `internal/adapter/adapter.go` — `Adapter` interface + canonical event
- `internal/adapter/claudecode/` — refactor existing hook code to
  implement the interface
- `internal/proxy/server.go` — HTTP server, OpenAI + Anthropic + Gemini
  endpoints with passthrough behavior
- `arbiter proxy` subcommand, foreground + background flags
- `arbiter init` detects installed CLIs and emits configuration hints

Done when: pointing Codex at `localhost:7777/v1` gets responses
identical (modulo arbiter logging) to direct OpenAI calls.

## Step 12 — Tool gating in proxy mode ◯

**Goal**: When the proxy sees a tool-use block in a model response, it
runs the same decide pipeline as hook mode. Streaming and non-streaming
both supported.

Deliverables:
- Response inspection: parse tool-use blocks pre-return
- Apply `internal/decide` to each tool call as a synthetic
  `CanonicalEvent`
- On deny: rewrite the response so the CLI sees a "tool blocked" result
  rather than the requested tool-use
- On escalate: pause the stream until user replies, then return
  approve/deny
- Streaming: SSE chunk buffering + late-injection of decision

Done when: Codex tries to run a destructive tool, arbiter blocks it via
`rules.md`, and Codex receives a synthetic "tool refused: <reason>"
result allowing it to recover gracefully.

## Step 13 — Model routing + cross-provider translation + budget ◯

**Goal**: `rules.md` `## Model routing` is honored. Tasks classified.
Tokens + cost tracked. Cross-provider translation works for the common
cases.

Deliverables:
- `internal/proxy/classify/` — heuristic + Haiku LLM classifier with cache
- `internal/proxy/route/` — applies `rules.md` routing rules
- `internal/proxy/translate/` — Anthropic ↔ OpenAI ↔ Gemini converters
  for messages, tools, system prompts
- `internal/proxy/budget/` — soft + hard limits, notify on threshold
- `internal/usage/` — JSONL ledger
- `arbiter usage` reporter
- Test suite: cross-provider translation parity (Claude → OpenAI →
  Claude round-trips a representative tool call without semantic loss)

Done when: a session tagged `read-only-summarization` calling for
`gpt-5` is rewritten to `gemini-2.5-flash`, succeeds, and the cost
delta shows up in `arbiter usage`.

## Step 14 — `delegate_to` cross-CLI handoff ◯

**Goal**: A running session can hand a sub-task to another CLI via the
`delegate_to` MCP tool, get a structured result back.

Deliverables:
- `internal/tools/delegate_to.go`
- Headless invocation modes for each CLI (e.g.,
  `claude --print`, `codex --json`, ...)
- Context handoff format (system prompt + task + budget)
- Output collection + summarization

Done when: from a Claude Code session, calling
`delegate_to(cli="codex", task="...")` runs Codex non-interactively,
captures its work, and returns a clean summary into Claude's context.

---

## Open questions

These should be resolved before or during the relevant step.

| # | Question | Step | Status |
|---|---|---|---|
| 1 | License — MIT vs Apache-2 vs proprietary? | 1 | Pending |
| 2 | Public repo or private until v1? | 1 | Pending |
| 3 | Org/owner namespace on npm + GitHub | 8 | Pending |
| 4 | Telegram-only or Discord/Slack from v1? | 5 | Telegram only for v1 |
| 5 | Should `arbiter init` overwrite existing rules.md or refuse? | Ongoing | Refuse without `--force` |
| 6 | How to handle multi-account Claude Code (different ANTHROPIC_API_KEYs per project)? | 4 | Per-project env override in rules.md |
| 7 | Concurrency: two parallel hook invocations same project — race on cache? | 2 | Mutex on parsed config; safe |
| 8 | What if `cwd` is outside any project tree? | 2 | Use global default rules; warn |
| 9 | API keys storage in proxy mode — file vs OS keychain? | 11 | Prefer keychain (`go-keyring`) |
| 10 | Proxy port collision (Ollama 11434, LM Studio, etc.) | 11 | Default 7777, configurable in `~/.config/godx-arbiter/config.yaml` |
| 11 | Streaming SSE buffering for tool-gating — latency target? | 12 | < 100ms added at first tool-use boundary |
| 12 | Antigravity API surface stability | 11+ | Adapter best-effort; revisit when API stabilizes |
| 13 | Cross-provider translation gaps (e.g., Anthropic `thinking`) | 13 | Document + fail-loud rather than silent drop |
| 14 | Per-CLI vs per-project budget — which wins on conflict? | 13 | Per-project overrides per-CLI; per-day hard limit always enforced |

## Non-goals (v1)

- GUI / web dashboard
- Team policy server (multi-user)
- SOC2 audit logging
- Sandboxing tool execution itself (we approve/deny; Claude Code executes)
- Replacing Claude Code's permission system entirely (we layer on top)

## Success criteria for v1

1. After `npm i -g godx-arbiter && arbiter init`, a developer running
   `claude --dangerously-skip-permissions` has working coordination
   without further config.
2. Fast-path p50 latency < 10ms.
3. Slow-path p50 latency < 1.5s using Haiku.
4. Cost per decision < $0.001 average.
5. Zero false-deny on the curated `safe-bash-allowlist` corpus.
6. Survives 1000 consecutive hook invocations without leak / hang.

## Stretch goals (post-v1)

- Browser MCP integration: arbiter inspects DOM screenshots to decide
  on UI-touching tool calls
- Time-bounded approvals: "approve all `Edit` in `src/foo/` for 30
  minutes" — useful for focused refactor sessions
- Diff-aware deny: "deny if the edit introduces a `// CRITICAL` comment
  removal"
- Team rules.md sync: pull rules.md from a shared Git repo on `arbiter doctor`
