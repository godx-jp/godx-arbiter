# `arbiter run` — autonomous task driver

`arbiter run` spawns a Claude Code (or codex / gemini / antigravity)
session headlessly for one task, streams the agent's work back to your
terminal in real time, and exits with a documented status code.

It's the answer to "tôi muốn một command tự gõ `claude` rồi nhập prompt
rồi chờ nó xong" — but built on `claude --print --output-format
stream-json` instead of tmux + send-keys, because **stream-json gives
us a deterministic completion signal** that auto-typing never can.

This doc covers: when to use it, what it preserves vs the interactive
TUI, the safety model, and recipes.

## Quick start

```bash
arbiter run -- "summarize the last week of git activity"
```

That's it. arbiter:

1. Resolves `claude` on `$PATH` and spawns it with
   `claude --print --output-format stream-json --verbose` (plus any
   passthrough flags you set).
2. Pipes your prompt in via stdin — no shell, no `send-keys`, no race
   conditions.
3. Renders Claude's text deltas + tool calls live to your terminal.
4. Tees the raw stream-json to
   `~/.config/godx-arbiter/runs/<id>.jsonl`.
5. Records start + end rows in the eventlog so `arbiter explain
   --last -v` works.
6. Exits when Claude emits `message_stop` (or with an error code if
   the run failed / timed out / got cancelled).

## Why no tmux at v0.1

Your first instinct (and a reasonable one): "open a tmux pane, type
`claude`, type the prompt, watch."

The problem isn't tmux — it's `send-keys`. There's no boundary that
tells you "the shell prompt is ready" or "claude has finished
initializing and is waiting for input". Every implementation falls
back to:

- `sleep 2` after spawning the pane
- regex-scrubbing the rendered terminal looking for the prompt char
- praying the local shell isn't slow today

By contrast, `claude --print --output-format stream-json`:

- Reads the entire prompt from stdin atomically — there's no "did the
  keys arrive?" question.
- Emits a structured `message_stop` event when the assistant is done.
- Exits with the agent loop's actual status — non-zero on failure,
  zero on success.

We don't lose anything important by skipping tmux at v0.1. The cases
where tmux genuinely helps are **detach / reattach** (long-running
task, close the laptop, come back tomorrow). That's a separate UX
feature; tracked as the **T2** tier below and ships in v0.2 with tmux
as the session host.

## Fidelity tiers

The other concern people raise: "claude --print không phải claude code
thật, nó là session hời hợt".

Here's the actual table of what `--print` does and doesn't preserve:

| Thành phần | `--print` có giữ? |
|---|---|
| `~/.claude/settings.json` hooks (PreToolUse, etc) | ✅ |
| `~/.claude/settings.json` `mcpServers` | ✅ |
| `CLAUDE.md` ở cwd + ancestors | ✅ |
| `--allowedTools` / `--disallowedTools` / `--permission-mode` | ✅ (passthrough) |
| `--mcp-config FILE` | ✅ (passthrough) |
| Auth từ env / keychain / `claude /login` | ✅ |
| Full agent loop (multi-turn tool use) | ✅ |
| Compaction khi context vượt giới hạn | ✅ |
| Skills / plugins từ `~/.claude/plugins/` | ✅ |
| Todo list management trong loop | ✅ (in-memory) |
| **Session resume** | ❌ default — fix bằng `--resume <id>` / `--continue` |
| TUI / `/commands` / Plan Mode UI | ❌ by design — output là JSON stream |
| Live attach để intervene mid-flight | ❌ v0.1; xem T2 |

→ The only real "hời hợt" gap is session continuity. Fix:

```bash
arbiter run -- "start the refactor"          # creates session
arbiter run --continue -- "continue with auth.go"
arbiter run --resume run-20260507-103000-7c -- "and then commit"
```

`--resume` validates the cwd matches the original run's cwd (refuses
otherwise so you don't end up reading the wrong CLAUDE.md). Override
with `--force-resume`.

| Tier | Transport | Fidelity vs Claude Code | Khi nào dùng |
|---|---|---|---|
| **T1 — v0.1 ship** | `claude --print --output-format stream-json` + full flag passthrough + `--resume` | ~95% (mất TUI, mất live attach; tools/hooks/MCP/skills/CLAUDE.md/auth giữ đủ) | Mặc định cho mọi task headless |
| **T2 — v0.2 follow-up** | T1 + tmux pane mirror để watch/attach | T1 + reattach UX | Long-running task, user muốn pop in xem progress |
| **T3 — v0.3+** | Real interactive `claude` qua PTY | 100% (full TUI) | Chỉ khi T2 không đủ — chưa có use case rõ |

## Command reference

See [CLI.md `arbiter run`](CLI.md#arbiter-run) for the full flag
table. The most-used subset:

```
arbiter run [flags] -- "<task prompt>"
arbiter run [flags] --task-file PATH
arbiter run [flags] --task-stdin

# Bookkeeping:
arbiter run --list [-n N] [--json]
arbiter run --resume-last           # shorthand for --continue
```

Common flags:

- `--cli claude|codex|gemini|antigravity` — pick the delegate. Claude
  is the default; the others use raw text capture (no streaming).
- `--cwd PATH` — pin working directory (defaults to `$PWD`).
- `--timeout 30m` — hard wall-clock cap.
- `--output stream|final|json` — how to render the events.
- `--quiet` — suppress live render; final summary still prints.
- `--resume ID` / `--continue` — resume a prior session.
- `--allowed-tools`, `--denied-tools`, `--permission-mode`,
  `--mcp-config`, `--add-dirs`, `--model` — passthrough to Claude
  Code.
- `--inherit-env` — pass the caller's full env instead of the
  curated allowlist (useful for one-offs).
- `--unsafe-skip-permissions` — gated; see Safety below.
- `--no-arbiter-hooks` — disable arbiter's own gating for this run
  (dev iteration only).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | `message_stop` + final assistant text. Task succeeded. |
| 1 | Child process exited non-zero. |
| 2 | Argument / config error (no API key, unknown CLI, refused unsafe flag, refused --resume cwd mismatch). |
| 124 | Timeout — matches `timeout(1)` convention. |
| 130 | User-interrupted (Ctrl-C → SIGTERM → 5s grace → SIGKILL on the child's process group). |

## Safety model

`arbiter run` is a fast path for scripted automation, which is
exactly the surface you want to lock down. The hardening:

1. **PATH hijack**: `exec.LookPath` resolves the child binary; the
   resolved absolute path is logged in the eventlog. If `claude`
   resolves to anything whose basename isn't `claude` (or `claude.exe`
   on Windows), arbiter refuses with exit 2.
2. **Process-group teardown**: the child runs in its own process
   group. Cancel / timeout / SIGINT triggers SIGTERM to the group,
   waits 5s, then SIGKILL. MCP servers Claude Code spawned go down
   with it.
3. **Env curation**: by default the child inherits only
   `PATH HOME USER SHELL LANG LC_ALL TERM ANTHROPIC_API_KEY
   ANTHROPIC_AUTH_TOKEN OPENAI_API_KEY GOOGLE_API_KEY`. `.env`-loaded
   variables don't leak. `--inherit-env` opts out.
4. **`--dangerously-skip-permissions` is triple-gated**:
   1. The flag itself is named `--unsafe-skip-permissions` — long and
      ugly on purpose.
   2. `GODX_ARBITER_ALLOW_UNSAFE=1` must also be set in env.
   3. rules.md frontmatter `forbid_unsafe_run: true` vetoes everything
      regardless. (Document, not yet enforced — coming with v0.1.1
      when rules.md frontmatter parser learns the field.)
   4. A red stderr banner prints on every unsafe spawn.
   5. Eventlog records `decision: "unsafe-spawn"` so `arbiter doctor`
      / `arbiter logs` can flag it.
5. **Recursion fuse**: `ARBITER_RUN_DEPTH` env counts hops. arbiter
   refuses if depth > 2. Stops accidental fork bombs from a delegated
   subtask that itself calls `arbiter run`.
6. **Resume cwd validation**: `--resume <id>` cross-checks the
   original cwd against the current cwd (because Claude Code reads
   `CLAUDE.md` + `settings.json` relative to cwd; resuming from the
   wrong project is a foot-gun). `--force-resume` overrides.

## Persistence

Three on-disk artifacts per run:

```
~/.config/godx-arbiter/
├── runs/
│   ├── index.jsonl                    # one start + one end row per run
│   └── run-20260507-103000-7c.jsonl   # raw stream-json transcript
└── events.jsonl                       # eventlog rows (Path: "run")
```

- `runs/index.jsonl` drives `arbiter run --list` and `--resume` cwd
  validation.
- The per-run JSONL is the raw stream-json — pipe through `jq` for
  custom analysis.
- The eventlog row reuses the run-id as `session_id`, so `arbiter
  explain run-20260507-103000-7c -v` shows the same run with the
  agent trace summarized.

Auto-prune is not enabled in v0.1 — the data is local and bounded
in size by your usage. `arbiter logs --runs --prune --older-than 30d`
is on the v0.1.1 follow-up list.

## Recipes

### Daily standup summary

```bash
arbiter run --cli=claude --quiet -- \
  "Summarize what changed in this repo since yesterday at 09:00. Use git log + git diff. 5 bullets max."
```

`--quiet` keeps your terminal clean; the final summary still prints.

### Long-running refactor with budget

```bash
arbiter run \
  --timeout=1h \
  --notify-on-done \
  --allowed-tools=Read,Glob,Grep,Edit \
  --denied-tools=Bash \
  -- "Rewrite internal/auth/ to use OS keychain everywhere. Don't touch tests; I'll add them."
```

Telegram pings when it's done (per `notify_channels` in your
`rules.md`). Claude can read + edit but can't shell out.

### Cross-CLI handoff for code generation

```bash
arbiter run --cli=codex --output=final --timeout=10m -- \
  "Implement the migration script described in plan.md"
```

Codex doesn't speak stream-json, so we capture raw stdout.

### Resume a session you started yesterday

```bash
arbiter run --list -n 5
# pick the id you want
arbiter run --resume run-20260506-153012-2a -- "now finish the test cases"
```

If you stay in the same project (`cwd`), this works without
`--force-resume`.

### Test the unsafe gate

```bash
arbiter run --unsafe-skip-permissions -- "x"
# → exit 2 with refusal text

GODX_ARBITER_ALLOW_UNSAFE=1 arbiter run --unsafe-skip-permissions -- "x"
# → red banner + runs
```

### Pipe the raw stream-json to jq

```bash
arbiter run --output=json --quiet --log-file /dev/null -- "say hi" | jq '.type'
```

Or read the per-run log file:

```bash
jq '.type' ~/.config/godx-arbiter/runs/run-*.jsonl | sort | uniq -c
```

## Comparison with `delegate_to`

`delegate_to` is the MCP tool for "Claude Code is mid-conversation and
wants to spawn a sub-task". `arbiter run` is the CLI equivalent for
"I'm at a shell and want to fire-and-watch a task".

Both share `internal/runner/cliflags.go` for the per-CLI invocation
table — adding a flag to one lights it up in the other automatically.

| | `delegate_to` (MCP) | `arbiter run` (CLI) |
|---|---|---|
| Caller | An LLM session | A human at a shell |
| Output | Single string returned to the caller | Live stream + log file |
| Default render | None (capture only) | Stream renderer to terminal |
| Concurrency | One per parent agent decision | One per shell invocation |
| Resume / continue | No (always fresh) | Yes (`--resume`, `--continue`) |

## Open follow-ups

- **T2 detach mode** — `arbiter run --detach NAME` should hand off to
  a tmux session you can `tmux attach -t arbiter-NAME` later. Tmux
  earns its keep here, where it didn't at the transport layer.
- **OpenAI / Gemini streaming** — both have stream-style APIs but no
  CLI equivalent yet. Falling back to `--output=final` is the honest
  v0.1.
- **rules.md `forbid_unsafe_run`** — frontmatter field that vetoes
  `--unsafe-skip-permissions` per project. Document, not enforced
  yet.
- **Web dashboard for run history** — `arbiter run --list` is
  command-line; a small TUI / web view is on the wishlist.
