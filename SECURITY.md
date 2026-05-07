# Security

This file describes godx-arbiter's threat model, what arbiter does and
does not protect, and how to report a security issue.

## Threat model

godx-arbiter sits between an AI coding CLI and your filesystem /
shell. It exists to keep an over-eager (or compromised) model from
running destructive operations on your machine.

**In scope:**

- A model proposing dangerous shell commands (`rm -rf`, `git push
  --force`, `curl ... | sh`).
- A model attempting to edit secret-bearing files (`.env`, `*.pem`,
  `*credentials*`).
- A compromised tool result tricking the model into approving an
  unsafe action.
- A model in `--dangerously-skip-permissions` mode that would
  otherwise have no human gate.

**Out of scope (explicit non-goals):**

- **Sandboxing tool execution itself.** arbiter approves/denies; the
  calling CLI executes. If the CLI process is compromised, arbiter
  can't help.
- **Defending against the model when arbiter's own model is the
  attacker.** The slow-path uses an LLM whose own outputs we trust;
  prompt injection on `rules.md`/`tool_input` could in principle steer
  the agent. Mitigations: low-temperature decisions, fast-path regex
  for absolute denials, eventlog for post-hoc review.
- **Network-level attacks** on the Anthropic / OpenAI / Gemini APIs.
  The proxy passes provider auth headers through unchanged; HTTPS
  pinning is the SDK / OS's job.
- **Secrets in `rules.md`.** The agent will quote them back to itself
  in tool calls and escalation messages. Don't put secrets in
  `rules.md` — see RULES_SPEC.md "Anti-patterns".
- **Multi-user / team policy enforcement.** This is a single-user
  local tool. Team rule sync is a stretch goal.
- **SOC2 / audit logging.** The eventlog feeds `lookup_history`, not
  legal audit. It's append-only and best-effort.

## Trust boundaries

```
       Untrusted
       ┌─────────────────────────────────────────┐
       │  Model (Claude / GPT / Gemini)           │
       │  Tool input (whatever the model returns) │
       │  Network / upstream APIs                 │
       └────────────┬────────────────────────────┘
                    │ JSON over stdin / HTTP
                    ▼
       Trusted (arbiter binary)
       ┌─────────────────────────────────────────┐
       │  config.LoadFromCwd                      │
       │  policy.Eval (regex on validated YAML)   │
       │  agent (LLM call with cache_control)     │
       │  tools (registry-dispatched)             │
       └────────────┬────────────────────────────┘
                    │ stdout JSON
                    ▼
       Calling CLI (also trusted)
```

The boundary is the JSON parser. Anything inside the binary trusts
that the parsed structs are well-formed. Anything outside is
adversary-controlled.

## Hardening done

- **Input length** caps on `read_file` (8 KiB default), `delegate_to`
  output (16 KiB), eventlog input summary (240 chars).
- **Path escape protection** on `read_file` — refuses paths outside
  the project root.
- **Binary file refusal** on `read_file` — UTF-8 only.
- **Regex compilation at load time** — bad regex in `policy.yaml`
  drops the rule with a warning rather than crashing.
- **JSON parse errors** never crash arbiter (panic-recover at the
  hook entry).
- **Subprocess invocation** in `delegate_to` uses `exec.Command`, not
  shell-split — no command injection via `task` content.
- **API keys** prefer the OS keychain over env vars; `arbiter auth
  set` reads from stdin so they don't appear in shell history.
- **Settings.json mutation** always backs up first.
- **Eventlog permissions** 0644 on file, 0755 on directory — readable
  by the same user only.
- **Credentials fallback file** 0600.

## Hardening not done (known)

- The proxy holds provider API keys in memory; a process dump on a
  shared host could read them. Move to keychain-only or per-CLI
  isolation if this matters.
- `rules.md` is loaded verbatim into the agent's system prompt with no
  size cap. A sufficiently large `rules.md` (>100k tokens) could
  exhaust the model's context.
- The eventlog isn't redacted. If your tool inputs include secrets,
  they end up on disk. See [docs/EVENTLOG.md](docs/EVENTLOG.md#privacy).
- The Telegram channel polls `getUpdates` — multiple arbiter
  processes against the same bot will see split delivery.

## Reporting a vulnerability

Please **don't** file a public issue for security bugs. Email
**security@godx.jp** with:

1. A description of the issue and its impact.
2. A reproduction (small POC if possible).
3. Affected versions (`arbiter version`).
4. Whether you'd like credit, and under what name.

We'll acknowledge within 3 business days and aim to ship a fix within
14 days for critical issues, 30 days for non-critical. We coordinate
disclosure date with the reporter.

## Rotating a leaked key

API keys end up exposed surprisingly often (terminal screenshares,
chat transcripts, PR comments, accidentally-committed files). The fix
is always:

1. Rotate the key at the provider's console (Anthropic / OpenAI /
   Google).
2. `arbiter auth set <provider>` with the new value.
3. Audit recent activity — Anthropic and OpenAI both expose per-key
   token usage in their consoles. Anomalous usage near the leak window
   is the canary.

For the Telegram bot token: revoke and re-mint at @BotFather, then
`arbiter auth set telegram`.
