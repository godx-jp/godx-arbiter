# godx-arbiter

LLM-based decision arbiter for AI coding CLIs. Reads per-project rules
written in Markdown, intercepts tool calls (via hooks or LLM proxy), and
decides **approve / deny / ask** on the agent's behalf — plus picks the
right model for each task to optimize cost.

Works across:

- **Claude Code** (Anthropic) — hook + MCP integration
- **Codex CLI** (OpenAI) — proxy mode
- **Gemini CLI** (Google) — proxy mode
- **Antigravity** (Google) — proxy mode

## Quick start

```bash
npm install -g godx-arbiter            # or: curl -sSL https://godx-arbiter.dev/install.sh | bash
arbiter init                           # scaffold .arbiter/ + register hooks in ~/.claude/settings.json
arbiter auth set anthropic             # store the slow-path agent's API key in your OS keychain
arbiter doctor                         # verify
```

Then run any tool call with Claude Code; arbiter sits in front.

## Where to go next

**Get started**

- [Install](INSTALL.md) — npm, curl, manual.
- [CLI reference](CLI.md) — every subcommand and flag.
- [Troubleshooting](TROUBLESHOOTING.md) — common issues and fixes.

**Configure**

- [All config files](CONFIG.md) — global, per-project, environment.
- [rules.md spec](RULES_SPEC.md) — per-project Markdown rules.
- [policy.yaml spec](POLICY_SPEC.md) — regex fast-path.
- [Skills system](SKILLS.md) — reusable prompt fragments.

**Understand**

- [Architecture](ARCHITECTURE.md) — system overview, the four layers.
- [Decision flow](DECISION_FLOW.md) — the decide loop step-by-step.
- [Slow-path agent](AGENT.md) — Anthropic SDK + tool loop internals.
- [Eventlog](EVENTLOG.md) — schema for `events.jsonl` + querying.

**Multi-CLI**

- [Adapters + proxy](MULTI_CLI.md) — Mode A vs Mode B + capability matrix.
- [Model routing](MODEL_ROUTING.md) — task-aware routing + budget.
- [MCP tools](MCP_TOOLS.md) — what the agent can call mid-decision.

**Project**

- [Roadmap](ROADMAP.md) — what's shipped, what's next.
- [Decisions log](DECISIONS.md) — ADRs.
- [Contributing](https://github.com/godx-team/godx-arbiter/blob/main/CONTRIBUTING.md)
- [Security](https://github.com/godx-team/godx-arbiter/blob/main/SECURITY.md)
- [Changelog](https://github.com/godx-team/godx-arbiter/blob/main/CHANGELOG.md)
