# Changelog

All notable changes are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
follow [SemVer](https://semver.org/spec/v2.0.0.html). Until 1.0.0,
minor versions may break.

## Unreleased

### Added — `arbiter run` autonomous task driver

- New top-level `arbiter run` subcommand. Spawns Claude Code (or
  codex / gemini / antigravity) headlessly for one task, streams the
  agent's work back to the user's terminal in real time, exits with a
  documented status code (0/1/2/124/130).
- Transport: `claude --print --output-format stream-json` rather than
  tmux + `send-keys`. The `message_stop` event is the deterministic
  completion signal — see docs/RUN.md for the full rationale.
- Fidelity passthrough flags (`--resume`, `--continue`,
  `--allowed-tools`, `--denied-tools`, `--permission-mode`,
  `--mcp-config`, `--add-dirs`, `--model`) bridge the gap to
  interactive Claude Code: hooks, MCP servers, CLAUDE.md, skills,
  auth all flow through.
- Safety: triple-gated `--unsafe-skip-permissions` (env +
  banner + eventlog), curated child env allowlist, PATH-hijack guard,
  process-group teardown with 5s grace SIGTERM → SIGKILL, recursion
  fuse via `ARBITER_RUN_DEPTH`.
- Persistence: per-run JSONL log at
  `~/.config/godx-arbiter/runs/<id>.jsonl`, append-only `index.jsonl`
  for `arbiter run --list`, eventlog rows with `Path: "run"`.
- Implementation: new `internal/runner` package
  (runner/spec/streamjson/render/cliflags/index). `delegate_to` MCP
  tool refactored to share the same engine — single source of truth
  for per-CLI flag tables.
- Docs: new `docs/RUN.md` (design + risks + recipes + fidelity tiers),
  `docs/CLI.md` `arbiter run` section, cross-links from `MULTI_CLI.md`
  and `AGENT.md`.
- Tests: 13 unit tests in `internal/runner/`, 6 integration tests
  driving the binary against a fake-claude shell script in
  `internal/runner/testdata/bin/`.

## 0.1.0 — 2026-05-06

The first end-to-end alpha. Closes the original 14-step ROADMAP plus
the production-polish follow-ups.

### Pipeline (steps 1–14)

- **Step 1** — repo scaffold + CLI shim. `cmd/arbiter`,
  `internal/hookio` with the modern
  `hookSpecificOutput.permissionDecision` shape.
- **Step 2** — project detection + config loading.
  `internal/projectfind`, `internal/config` (rules / policy /
  cache / frontmatter / project) with mtime-keyed parse cache.
- **Step 3** — fast-path policy engine.
  `internal/policy` evaluating deny → allow → to_agent → default.
- **Step 4** — slow-path agent. `internal/agent` wraps
  `anthropic-sdk-go` with a hand-rolled tool loop, mockable LLM
  driver, max-iters + timeout fallback. Cache-control on the
  system block (~60% input-token savings on repeats).
- **Step 5** — notification + escalation. `internal/notify` with
  log / desktop / telegram / webhook channels, `escalate_to_user`
  tool, quiet-hours suppression, 60s dedup.
- **Step 6** — MCP server. `internal/mcp` stdio JSON-RPC 2.0 with
  initialize, tools/list, tools/call.
- **Step 7** — skills system. `internal/skills` resolves
  `@include skill:<name>` directives with project / global /
  built-in fallback. 5 built-in skills.
- **Step 8** — distribution. `npm/` wrapper, `install.sh`, GitHub
  Releases workflow with cross-compile + checksums + npm publish.
- **Step 9** — `arbiter explain` + replay. `internal/eventlog`.
- **Step 10** — docs site. mkdocs-material at `mkdocs.yml`, deploy
  workflow at `.github/workflows/docs.yml`.
- **Step 11** — adapter framework + proxy server. `internal/adapter`
  for Claude Code / Codex / Gemini / Antigravity; `internal/proxy`
  HTTP server with PreForward / PostResponse / StreamTransform
  hooks.
- **Step 12** — proxy tool gating, both non-streaming and streaming
  (SSE). Anthropic + OpenAI rewrites work; Gemini passes through.
- **Step 13** — model routing + cross-provider translation + budget.
  `internal/proxy/{classify, route, translate, budget}` with
  Anthropic ↔ OpenAI ↔ Gemini converters, heuristic classifier,
  soft + hard budget thresholds, per-day cost reset.
- **Step 14** — `delegate_to` cross-CLI handoff.
  `internal/tools/delegate_to.go` runs claude / codex / gemini /
  antigravity non-interactively for sub-tasks.

### Subcommands

`hook` (pretool / posttool / notification / stop) · `init` · `doctor`
(+ `--notify-test`, `--json`) · `uninstall` · `auth` (set/get/list/
delete) · `mcp` · `proxy` · `usage` · `logs` (with --tail / --session
/ --tool / --decision / --since / --json) · `explain` · `version`.

### Storage + secrets

- API keys via OS keychain (`go-keyring`) with env var override and a
  plain-text `$GODX_ARBITER_HOME/credentials` fallback.
- Eventlog at `~/.local/share/godx-arbiter/events.jsonl`.
- Usage ledger at `~/.local/share/godx-arbiter/usage.jsonl`.
- Global config at `~/.config/godx-arbiter/config.yaml`.

### Quality gates

- 19 Go packages, all green under `go test -race -count=1`.
- `go vet` clean.
- `.golangci.yml` baseline; CI gates lint + vet + test +
  cross-compile + smoke + docs build.
- Integration test suite builds the binary and drives the hook
  subcommand end-to-end (`cmd/arbiter/integration_test.go`).

### Documentation

`docs/`: ARCHITECTURE, DECISION_FLOW, RULES_SPEC, POLICY_SPEC,
MULTI_CLI, MODEL_ROUTING, MCP_TOOLS, INSTALL, ROADMAP, DECISIONS,
plus the new CLI / CONFIG / SKILLS / EVENTLOG / AGENT /
TROUBLESHOOTING references. Top-level CONTRIBUTING / SECURITY /
LICENSE.

### Notes

- Core invariant: **arbiter must never break a calling session**
  (ADR-005). Default `on_error: approve` for fail-open semantics.
- Cost target: **< $0.001 per decision** averaged across a session
  (achieved with Haiku + prompt caching + heavy fast-path use).
- License: MIT.
