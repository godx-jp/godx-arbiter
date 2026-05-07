# Changelog

All notable changes are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
follow [SemVer](https://semver.org/spec/v2.0.0.html). Until 1.0.0,
minor versions may break.

## Unreleased

### Added — production polish round

- OpenAI streaming SSE tool gating
  (`internal/proxy/sse_openai.go`). Buffers
  `tool_calls.function.arguments` deltas across chunks, runs
  `policy.Eval` on the assembled call, rewrites arguments to a
  refusal payload on deny so the calling agent can recover.
- Gemini ↔ Anthropic / OpenAI translation
  (`internal/proxy/translate/gemini.go`). Cross-provider routing
  now works across all three vendors.
- OS keychain integration via `go-keyring`
  (`internal/auth`). New `arbiter auth set|get|list|delete`
  subcommand. Resolution chain: env var → keychain →
  `$GODX_ARBITER_HOME/credentials` plain-text fallback.
- `arbiter doctor --notify-test` — sends a live message via every
  available channel.
- `arbiter doctor --json` — stable machine-readable diagnostic
  schema.
- `arbiter uninstall` — removes hooks + MCP entries from
  `~/.claude/settings.json` with timestamped backup. `--dry-run`
  preview.
- `arbiter logs` — tail / filter the decision eventlog.
  `--tail`, `--session`, `--tool`, `--decision`, `--since`,
  `--json`.
- Quiet-hours filter + 60s dedup
  (`internal/notify/policy.go`). `notify_channels` is rewritten
  during the window so Telegram doesn't fire during sleep.
- Prompt caching on the agent's system block. `cache_control:
  ephemeral` on the rules.md + skills + action JSON cuts ~60% of
  input tokens for repeated decisions.
- Global config file
  (`$GODX_ARBITER_HOME/config.yaml` /
  `internal/config/global.go`) with proxy port, fallback rules,
  per-CLI mode, default notify settings.
- mkdocs site (`mkdocs.yml`, `docs/index.md`,
  `.github/workflows/docs.yml`).
- `.golangci.yml` + Makefile lint target + CI lint step.
- Integration tests build the binary and exercise the hook
  subcommand end-to-end (`cmd/arbiter/integration_test.go`).
- New docs: `CLI.md` (command reference), `CONFIG.md` (all
  configuration files), `SKILLS.md` (skill system + library),
  `EVENTLOG.md` (schema + querying), `AGENT.md` (slow-path
  internals), `TROUBLESHOOTING.md`. Top-level `CONTRIBUTING.md`,
  `SECURITY.md`, this `CHANGELOG.md`.

## 0.1.0 — initial scaffold (May 2026)

The first end-to-end pipeline. Closes the original 14-step ROADMAP.

### Added

- Step 1 — repo scaffold + CLI shim. `cmd/arbiter`,
  `internal/hookio` with the modern
  `hookSpecificOutput.permissionDecision` shape.
- Step 2 — project detection + config loading.
  `internal/projectfind`, `internal/config` (rules / policy /
  cache / frontmatter / project) with mtime-keyed parse cache.
- Step 3 — fast-path policy engine.
  `internal/policy` evaluating deny → allow → to_agent → default.
- Step 4 — slow-path agent. `internal/agent` wraps
  `anthropic-sdk-go` with a hand-rolled tool loop, mockable LLM
  driver, max-iters + timeout fallback.
- Step 5 — notification + escalation. `internal/notify` with
  log / desktop / telegram / webhook channels, `escalate_to_user`
  tool.
- Step 6 — MCP server. `internal/mcp` stdio JSON-RPC 2.0 with
  initialize, tools/list, tools/call.
- Step 7 — skills system. `internal/skills` resolves
  `@include skill:<name>` directives with project / global /
  built-in fallback.
- Step 8 — distribution. `npm/` wrapper, `install.sh`, GitHub
  Releases workflow with cross-compile + checksums.
- Step 9 — `arbiter explain` + replay. `internal/eventlog`.
- Step 10 — docs site. (Initially deferred; see Unreleased.)
- Step 11 — adapter framework + proxy server. `internal/adapter`
  for Claude Code / Codex / Gemini / Antigravity;
  `internal/proxy` HTTP server with PreForward / PostResponse
  hooks.
- Step 12 — proxy tool gating. Non-streaming response rewrite for
  Anthropic + OpenAI. Streaming gating for Anthropic. (OpenAI
  streaming added in Unreleased.)
- Step 13 — model routing + cross-provider translation + budget.
  `internal/proxy/{classify, route, translate, budget}` with
  Anthropic ↔ OpenAI translation, heuristic classifier, soft +
  hard budget thresholds.
- Step 14 — `delegate_to` cross-CLI handoff.
  `internal/tools/delegate_to.go` runs claude / codex / gemini /
  antigravity non-interactively for sub-tasks.

### Notes

- Core invariant: **arbiter must never break a calling session**
  (ADR-005). Default `on_error: approve` for fail-open semantics.
- Cost target: < $0.001 per decision averaged across a session
  (achieved with Haiku + prompt caching + heavy fast-path use).
