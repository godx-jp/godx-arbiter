# godx-arbiter

LLM-based decision arbiter for AI coding CLIs. Reads per-project rules
written in Markdown, intercepts tool calls (via hooks or LLM proxy), and
decides **approve / deny / ask** on the agent's behalf — plus picks the
right model for each task to optimize cost. Works across:

- **Claude Code** (Anthropic) — hook + MCP integration
- **Codex CLI** (OpenAI) — proxy mode
- **Gemini CLI** (Google) — proxy mode
- **Antigravity** (Google) — proxy mode

One binary. One `.arbiter/rules.md` per project. All your CLIs coordinated.

## The problem

Running any agentic CLI in `--dangerously-skip-permissions` mode removes
prompt fatigue but also removes the safety net. The agent can `rm -rf`
something it shouldn't, push to `master`, or rewrite a critical config —
and you find out after the fact. The opposite extreme (manual approval
for every tool call) slows real work to a crawl.

Worse, when you use multiple CLIs (Claude for one task, Codex for
another), each has its own permission model, its own model selection,
its own token bill. There is no shared judgment layer.

godx-arbiter is that layer: a **project-aware AI agent** that
understands your project's rules (in plain Markdown), decides on the
calling CLI's behalf, escalates when needed, and routes tasks to the
right model/CLI to optimize tokens.

## How it works (60-second version)

1. Each project gets `.arbiter/rules.md` — plain-English rules a human
   reads naturally and an LLM follows naturally.
2. Arbiter integrates with each CLI in the most native mode it
   supports:
   - **Claude Code**: hook into `PreToolUse` / `Notification` / `Stop`
     via `~/.claude/settings.json`, plus MCP for decision-support
     tools.
   - **Codex / Gemini / Antigravity**: run arbiter as a local LLM
     proxy. The CLI sends API requests to arbiter; arbiter inspects,
     decides, optionally re-routes to a different model, then forwards.
3. On every gated event, arbiter checks fast-path regex (`policy.yaml`).
   Cheap match returns immediately. Otherwise spawns an LLM sub-agent
   with the project's `rules.md` + decision-support tools and lets it
   decide.
4. In proxy mode, arbiter additionally classifies the task and may
   re-route to a cheaper / better model per `rules.md` routing config —
   tracking token cost across the session.
5. If the agent is unsure, it calls `escalate_to_user` → Telegram /
   desktop notification → you reply, or it falls back per `rules.md`
   policy on timeout.

## Stack

| Layer | Tech | Why |
|---|---|---|
| Core CLI + agent | **Go** (single static binary) | Compiled, source not exposed in distribution; single-binary install |
| LLM | Anthropic Go SDK (raw) | Hand-rolled tool loop — full control over agent |
| MCP server | Go (`mark3labs/mcp-go` or hand-rolled stdio) | Same code, two interfaces |
| Distribution | **npm wrapper** + GitHub Releases | `npm i -g godx-arbiter` downloads platform binary; also direct curl install |
| Project config | Markdown + YAML | Human-friendly; LLM-readable |

## Status

Alpha. Steps 1–14 of the roadmap landed (full pipeline: fast-path
policy, slow-path Anthropic agent, MCP server, notify channels, skills,
adapter framework, LLM proxy with tool gating, model routing, cross-
provider translation, budget tracking, `delegate_to`, `arbiter explain`,
distribution scaffolding). See [docs/ROADMAP.md](docs/ROADMAP.md).

## Documentation

**Get started**

| Doc | Purpose |
|---|---|
| [docs/INSTALL.md](docs/INSTALL.md) | Install paths: npm, curl, manual binary |
| [docs/CLI.md](docs/CLI.md) | Authoritative reference for every `arbiter` subcommand |
| [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) | Common issues and their fixes |

**Configure**

| Doc | Purpose |
|---|---|
| [docs/CONFIG.md](docs/CONFIG.md) | All configuration: global config, env vars, settings.json |
| [docs/RULES_SPEC.md](docs/RULES_SPEC.md) | Spec for project-level `.arbiter/rules.md` |
| [docs/POLICY_SPEC.md](docs/POLICY_SPEC.md) | Spec for fast-path `.arbiter/policy.yaml` |
| [docs/SKILLS.md](docs/SKILLS.md) | Skills system + built-in library |

**Understand**

| Doc | Purpose |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Full system design, components, data flow |
| [docs/DECISION_FLOW.md](docs/DECISION_FLOW.md) | The decide loop: fast-path, agent, escalation, timeout |
| [docs/AGENT.md](docs/AGENT.md) | Slow-path internals — Anthropic SDK, tool loop, decision parser |
| [docs/RUN.md](docs/RUN.md) | `arbiter run` — autonomous task driver (Claude Code orchestration) |
| [docs/EVENTLOG.md](docs/EVENTLOG.md) | Eventlog schema + `arbiter logs` / `jq` recipes |

**Multi-CLI**

| Doc | Purpose |
|---|---|
| [docs/MULTI_CLI.md](docs/MULTI_CLI.md) | Adapters + LLM proxy mode for non-Claude CLIs |
| [docs/MODEL_ROUTING.md](docs/MODEL_ROUTING.md) | Task-aware model routing + token / cost optimization |
| [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md) | Catalog of decision-support tools |

**Project**

| Doc | Purpose |
|---|---|
| [docs/ROADMAP.md](docs/ROADMAP.md) | Multi-step build plan, milestones, open questions |
| [docs/DECISIONS.md](docs/DECISIONS.md) | ADR-style log of design decisions |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development workflow + how to add tools/channels/adapters |
| [SECURITY.md](SECURITY.md) | Threat model + vulnerability reporting |
| [CHANGELOG.md](CHANGELOG.md) | Version history |

## Quick install

```bash
npm i -g godx-arbiter         # or: curl -sSL https://godx-arbiter.dev/install.sh | bash
arbiter auth set anthropic    # store API key in OS keychain
arbiter init                  # writes ~/.claude/settings.json hooks + .arbiter/rules.md template
arbiter doctor                # verifies hooks, binary, API key
arbiter doctor --notify-test  # confirm Telegram / desktop / webhook reach you
```

## License

MIT (see [LICENSE](LICENSE)).
