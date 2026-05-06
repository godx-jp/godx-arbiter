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

- [Architecture](ARCHITECTURE.md) — system overview, the four layers, and how data flows.
- [Decision flow](DECISION_FLOW.md) — the decide loop step-by-step.
- [rules.md spec](RULES_SPEC.md) — how to write per-project rules.
- [policy.yaml spec](POLICY_SPEC.md) — fast-path regex rules.
- [Multi-CLI](MULTI_CLI.md) — adapters, proxy mode, capability matrix.
- [Model routing](MODEL_ROUTING.md) — task-aware routing + budget control.
- [MCP tools](MCP_TOOLS.md) — what the agent can call mid-decision.
- [Install](INSTALL.md) — npm, curl, manual.
- [Roadmap](ROADMAP.md) — what's shipped, what's next.
- [Decisions log](DECISIONS.md) — ADRs.
