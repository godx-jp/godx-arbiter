# Design decisions log

ADR-style log of significant decisions. Each entry: what, why, what we
considered, when. Newest first.

---

## ADR-007 — Task-aware model routing & token budgets

**Date**: 2026-05-05
**Status**: Accepted

### Context

A typical coding session mixes high-effort and low-effort work.
Routing all of it to one tier of model is wasteful (always-Opus) or
risky (always-Haiku). Different CLIs have different default models, and
the user wants global cost discipline.

### Decision

In proxy mode, the arbiter classifies the task per request, applies
`rules.md` `## Model routing` rules, and rewrites the model parameter
before forwarding. Token + cost tracked per session in a local ledger.
Per-session and per-day budgets enforce hard/soft limits with
notifications.

### Rationale

- Same `rules.md` already covers per-project policy — extending it to
  routing keeps one source of truth.
- Hook mode CAN'T see model invocations (only tool calls); proxy mode is
  the only place where model rewriting is feasible. Document this
  limit.
- Cross-provider translation (Anthropic ↔ OpenAI ↔ Gemini) lets us route
  beyond a single provider's lineup.

### Tradeoffs accepted

- Format translation is lossy at edges (advanced features like
  Anthropic's `thinking` blocks don't map to OpenAI). Document and
  fail-loud rather than silently lose data.
- Classification has cost (~$0.0001 per LLM-classifier call). Cached
  aggressively; heuristic-first.

### See

- [docs/MODEL_ROUTING.md](MODEL_ROUTING.md) for full design.

---

## ADR-006 — Multi-CLI support: hook + proxy hybrid

**Date**: 2026-05-05
**Status**: Accepted

### Context

The user runs multiple agentic CLIs (Claude Code, Codex, Gemini,
Antigravity). Each has its own permission model, model selection, and
billing. There is no shared judgment / coordination layer. Initial
design was Claude-Code-only via hooks, which doesn't generalize.

### Decision

Support all CLIs through two complementary modes:

1. **Hook mode** (Mode A): use the CLI's native hook surface where
   rich enough. Today: Claude Code (full); Codex / Gemini (partial).
2. **Proxy mode** (Mode B): arbiter runs as a local LLM proxy on
   :7777. CLIs are configured to use this endpoint. Universal.

Both modes feed the same canonical event into `internal/decide`. CLI
specifics live in `internal/adapter/<cli>/`.

### Rationale

- Claude Code's hook system is a real asset — using only proxy mode
  would lose richness (Notification hook, Stop hook, etc.) and add
  unnecessary latency for tool-call gating.
- Other CLIs don't have equivalent hooks today; proxy mode is the only
  path that works for them.
- Hybrid is the only way to give all CLIs a uniform `rules.md`
  experience without dropping features.
- The proxy approach doubles as the natural place for model routing
  (ADR-007) — the arbiter sits on the API path, sees every model call.

### Alternatives considered

- **Hook-only, accept Claude-Code-only scope**: rejected — user
  explicitly wants multi-CLI.
- **Proxy-only**: would lose Claude Code's Notification + Stop hooks
  and add latency. Rejected.
- **Per-CLI separate binaries**: bad for ops; one binary keeps install
  + update simple.

### Tradeoffs accepted

- Adapter maintenance burden per CLI. Mitigation: keep adapters thin;
  most logic in core.
- Proxy mode requires holding provider API keys — security-sensitive;
  use OS keychain.
- Streaming through the proxy is fiddly to gate on tool-use; Step 12
  of the roadmap addresses this.

### See

- [docs/MULTI_CLI.md](MULTI_CLI.md) for capability matrix and adapter
  interface.

---

## ADR-005 — Default fail-open on internal errors (`on_error: approve`)

**Date**: 2026-05-05
**Status**: Accepted

### Context

When the arbiter binary itself fails (parse error, API down, internal
bug), what decision should it return?

### Decision

Default to `approve` (fail-open). Configurable per-project via
`on_error: deny` in `rules.md` front matter for paranoid setups.

### Rationale

- Failing closed would block all developer work whenever arbiter has a
  bug — unacceptable availability cost during alpha/beta.
- Claude Code's own permission system (when not using
  `--dangerously-skip-permissions`) provides a backstop.
- Users who want fail-closed semantics can opt in.

### Alternatives considered

- **Fail closed default**: rejected on availability grounds.
- **Last-known-good cache**: complex; deferred.

---

## ADR-004 — Distribution via npm wrapper + GitHub Releases

**Date**: 2026-05-05
**Status**: Accepted

### Context

Need to ship a Go binary in a way that's easy for the target audience
(developers using Claude Code, mostly Node-friendly).

### Decision

Primary: npm package `godx-arbiter` whose postinstall script downloads
the platform-appropriate prebuilt binary from GitHub Releases.

Secondary: `curl -sSL ... | bash` install script for non-Node users.

Tertiary: manual binary download.

### Rationale

- `npm i -g` is the most common install vector for Claude Code users.
- Pattern proven by esbuild, swc, biome, turbo — well-understood.
- The npm package is tiny (~5 KB), so npm cache + registry overhead is
  minimal.
- Source not shipped via npm — fits "dấu code" requirement.

### Alternatives considered

- **Pure Go install**: `go install` would expose source; rejected.
- **Homebrew tap**: nice for macOS but excludes Linux/Windows.
- **Docker image**: too heavy for a per-tool-call hook.

---

## ADR-003 — Go for the core binary

**Date**: 2026-05-05
**Status**: Accepted

### Context

Choice of implementation language for the arbiter binary.

### Decision

Go (1.22+). Use raw Anthropic Go SDK
(`github.com/anthropics/anthropic-sdk-go`) and hand-roll the tool loop.

### Rationale

- Single static binary — no runtime deps in user environment.
- Compiled — source not exposed in distribution (vs. interpreted JS/Python).
- Cold-start ~10ms vs Node ~80ms vs Python ~150ms; matters because
  this binary runs on every tool call.
- Stack consistency with famgia-admin (Go-heavy).
- Cross-compile trivial: `GOOS=linux GOARCH=arm64 go build`.

### Tradeoffs accepted

- Anthropic's official Agent SDK is Python/TS — we don't get that.
  Acceptable: the tool loop is ~200 lines of Go to write ourselves.
- MCP server in Go is less mature than TS but workable
  (`mark3labs/mcp-go` or hand-rolled stdio).

### Alternatives considered

- **TypeScript / Node**: best Agent SDK + MCP support, but Node startup
  latency is the killer for hook-on-every-call usage.
- **Python**: same startup cost, plus deps management user-side.
- **Rust**: similar perf to Go, smaller community for Anthropic SDK,
  steeper learning curve. Go wins on team alignment.

---

## ADR-002 — Name: `godx-arbiter`

**Date**: 2026-05-05
**Status**: Accepted

### Context

Need a name that is:
1. Standard enough to not look bespoke
2. Distinctive enough to not collide with major existing tools
3. Semantically apt for "watches Claude session, decides on its behalf"

### Decision

`godx-arbiter`. CLI command: `arbiter`. Project file: `.arbiter/`.

### Rationale

- "Arbiter" is precise: a thing that judges between options based on
  rules. That's exactly what this is.
- Distinctive in tech namespace: search "claude arbiter", "arbiter
  hook", etc. all clean.
- `godx-` prefix establishes provenance / org while keeping the bare
  command (`arbiter`) memorable.

### Alternatives considered

- **coordinator**: weak in AI-agent space; more associated with
  distributed-systems coordination (Zookeeper, Kafka).
- **orchestrator**: too common (LangGraph, AutoGen, Airflow); search
  noise high.
- **supervisor**: collides with Python `supervisord` — same domain, real
  confusion risk.
- **sentinel**: collides with HashiCorp Sentinel (policy-as-code, same
  domain as us).
- **gatekeeper**: collides with OPA Gatekeeper (Kubernetes policy).
- **maestro**: evocative but vague; "arbiter" is more precise for the
  decision aspect.

---

## ADR-001 — Per-project Markdown rules + LLM-based decision

**Date**: 2026-05-05
**Status**: Accepted

### Context

Several plausible designs for the decision engine:

1. Pure regex / declarative ruleset (e.g., OPA, Sentinel)
2. Single global LLM agent
3. Per-project rules in Markdown + LLM agent that reads them

### Decision

Option 3: each project has `.arbiter/rules.md` (free-form Markdown), and
an LLM agent reads it as system-prompt context to decide.

Plus: optional `policy.yaml` for fast-path regex rules (zero LLM cost
for the obvious cases).

### Rationale

- Different projects have wildly different rules: a sandbox is
  permissive, a production codebase is strict. Per-project config is
  essential.
- Markdown is the right format because:
  - Humans read + write it naturally
  - LLMs parse it natively
  - Same file documents the rules for humans AND configures the agent —
    no drift between docs and behavior
  - Lives in the project repo, version-controlled with the code
- Pure regex falls short on nuance (e.g., "more than 5 files in a
  refactor" or "if the edit removes a `// CRITICAL` comment").
- LLM-only would be too expensive + slow for the obvious cases — hence
  the fast-path.

### Tradeoffs accepted

- Cost per decision: ~$0.001 with Haiku. Fine for individual usage,
  may need to revisit for high-volume sessions.
- Latency: ~1-2s on slow path. Acceptable since most calls go fast-path.
- LLM nondeterminism: same rules.md + same action could decide
  differently across runs. Mitigation: low temperature, explicit
  reasoning logged for debug, eventlog enables consistency checks.

### Alternatives considered

- **OPA / Rego**: powerful but steep learning curve; Markdown rules are
  more accessible for the target audience.
- **Single global rules file**: sandbox vs production is a real tension;
  per-project wins.
- **Inline rules in CLAUDE.md**: rejected to keep concerns separated —
  CLAUDE.md is for Claude Code's own behavior; rules.md is for the
  arbiter's gate-keeping.

---

## Template for future ADRs

```
## ADR-XXX — <Title>

**Date**: YYYY-MM-DD
**Status**: Proposed | Accepted | Superseded by ADR-YYY | Deprecated

### Context
(What problem are we solving?)

### Decision
(What did we decide?)

### Rationale
(Why this option?)

### Alternatives considered
(What else, why rejected?)

### Tradeoffs accepted
(What are we giving up?)
```
