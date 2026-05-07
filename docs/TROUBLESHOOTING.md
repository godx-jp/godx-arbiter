# Troubleshooting

A grab-bag of issues users hit, with their fixes. Order: most common
first.

## Hook isn't firing

**Symptom**: Claude Code runs tools as if no policy exists. No
arbiter entries appear in `~/.local/share/godx-arbiter/events.jsonl`.

**Diagnose**:

```bash
arbiter doctor                    # check binary path + settings.json
ls -la ~/.claude/settings.json    # confirm file exists
jq '.hooks' ~/.claude/settings.json
```

**Fix**:

- If `hooks` block is missing, run `arbiter init`.
- If a project-local `.claude/settings.json` overrides the user-level
  one, merge or move the arbiter block into the project file (Claude
  Code reads the project version).
- If `arbiter` isn't on `PATH` from Claude Code's environment (it
  inherits your login shell, but tmux + non-login shells can drop it),
  put the absolute path in settings.json:

  ```json
  { "command": "/home/you/.local/bin/arbiter hook pretool" }
  ```

## "no ANTHROPIC_API_KEY" in the metadata

**Symptom**: hook output has `"path":"agent-stub"` and reasons like
"agent unavailable: no ANTHROPIC_API_KEY". Decisions effectively pass
through as the `on_error` fallback (`approve` by default).

**Fix**:

```bash
arbiter auth set anthropic         # stores in OS keychain
# or, if no keychain available
export ANTHROPIC_API_KEY=sk-ant-...
```

Verify with `arbiter auth list`.

## Slow-path takes >10s consistently

**Symptom**: every agent decision sits at 8–15s, occasionally
timing out per `on_timeout`.

**Diagnose**:

```bash
arbiter explain --last -v          # how many tool iters? which model?
arbiter usage --today              # token totals, model split
```

**Common causes**:

| Cause | Fix |
|---|---|
| Using Sonnet/Opus by default | Set `agent_model: claude-haiku-4-5-20251001` in front matter; Haiku is the documented default for a reason |
| Cold prompt cache every call | Make sure `rules.md` body doesn't change between calls — even whitespace tweaks invalidate cache |
| Agent calling 5+ tools | Tighten `rules.md` so it answers obvious questions without tools, or move them to `policy.yaml` fast-path |
| Network round-trip slow | `time curl https://api.anthropic.com/v1/messages` to isolate; consider proxy retries |

## "tool refused by godx-arbiter" appears in the model output

**Symptom**: when running through the proxy, the calling CLI sees a
synthetic refusal block instead of its requested tool call. This is
intentional: the proxy's tool gating denied the call based on
`policy.yaml`.

**Verify**:

```bash
arbiter logs --decision deny --tail
```

Find the matching event; the `reason` field gives the rule text.

**Fix**: either loosen the policy rule (if the deny is wrong) or fix
the calling CLI's tool input (if the deny is right).

## Telegram bot never replies

**Symptom**: notification appears but tapping a button has no effect.

**Diagnose**:

```bash
arbiter doctor --notify-test       # confirms the channel can deliver
```

**Common causes**:

| Cause | Fix |
|---|---|
| Bot privacy mode on | In BotFather: `/setprivacy` → Disable, so the bot reads group messages |
| Wrong chat id | The chat id should be a numeric long, not a username. Get it from `/getUpdates` after sending the bot a `/start` |
| Multiple arbiter processes polling | The Telegram Bot API only delivers each update once. Stop other instances or move to a webhook (future work; not yet shipped) |
| Bot blocked by Telegram | Old token rotated; mint a new one with BotFather and `arbiter auth set telegram` again |

## `escalation: timeout` even with channels configured

**Symptom**: agent picks `ask`, but the metadata shows
`"escalation":"timeout"` and the decision is the `on_timeout`
fallback.

**Why**: arbiter dispatches channels in order and waits for the first
reply. If the desktop channel runs first and "delivers" without a
reply mechanism (it's fire-and-forget), the dispatcher returns
`Timeout: true` immediately.

**Fix**: put the interactive channel first in
`notify_channels: [telegram, desktop, log]`. Desktop is best as a
secondary notice + log is the universal fallback.

## `arbiter init` overwrote my settings.json

**Symptom**: existing keys in `settings.json` are preserved, but you
want a clean rollback.

**Fix**: every `init` run writes a backup at
`~/.claude/settings.json.arbiter-backup-<ts>`. Restore the most recent
one:

```bash
ls -t ~/.claude/settings.json.arbiter-backup-* | head -1 \
  | xargs -I{} cp {} ~/.claude/settings.json
```

## go-keyring errors on Linux servers ("name is not activatable")

**Symptom**: `[arbiter auth] keychain lookup failed: The name is not activatable`.

**Why**: you're on a headless box (CI, container, SSH-only server)
without a running secret-service daemon (gnome-keyring or KeePassXC).

**Fix**: arbiter falls back to a plain-text file at
`$GODX_ARBITER_HOME/credentials` (mode 0600). The warning is logged
once per command and is otherwise harmless. To silence it permanently,
use env vars instead:

```bash
export ANTHROPIC_API_KEY=$(cat ~/.config/anthropic-key)
```

(Yes, this trades convenience for slightly less secret hygiene. On a
trusted server it's a reasonable trade.)

## Proxy: "budget hard limit reached"

**Symptom**: proxy returns `502 Bad Gateway` with the body
`budget hard limit reached: <reason>`.

**Why**: rules.md `## Budget` thresholds were hit.

**Fix**: any of:

- Wait for the daily window to reset (midnight UTC).
- Bump `daily_hard_usd` if the budget is genuinely too low.
- Switch to a cheaper model (`gemini-2.5-flash` for summarization,
  Haiku for edits).
- `export GODX_ARBITER_DISABLED=1` to bypass arbiter entirely for one
  emergency session.

## Eventlog growing fast

**Symptom**: `~/.local/share/godx-arbiter/events.jsonl` is hundreds
of MB.

**Diagnose**:

```bash
wc -l ~/.local/share/godx-arbiter/events.jsonl
jq -r .tool ~/.local/share/godx-arbiter/events.jsonl | sort | uniq -c | sort -rn
```

**Fix**: the file is append-only and arbiter doesn't rotate it. Run a
manual rotation or set up logrotate:

```
/home/you/.local/share/godx-arbiter/events.jsonl {
  weekly
  rotate 4
  size 100M
  compress
  copytruncate
}
```

## "agent did not emit ARBITER_DECISION line; refusing"

**Symptom**: every slow-path decision denies with this reason.

**Why**: the model isn't following the prompt's "End with one of …"
instruction. Most often this happens with a model that ignored the
system prompt structure (older model variants, custom fine-tunes).

**Fix**: confirm `agent_model` in front matter is a recent
Anthropic model. Haiku 4.5 follows the format reliably; very old
models or non-Anthropic models routed via proxy may not.

## "expected JSON on empty stdin too" failing in CI

**Symptom**: integration test asserts that empty stdin still produces
a fail-open allow JSON.

**Verify**: the test itself is documenting ADR-005; if the test
fails, the binary is exiting non-zero on empty stdin instead of
emitting fail-open JSON. Re-run with strace to confirm:

```bash
echo -n '' | /tmp/arbiter hook pretool ; echo "exit=$?"
```

Expected: stdout has a valid `hookSpecificOutput` JSON,
`permissionDecision: allow`, exit 0.

If it's failing, suspect a recent change to `failOpen` /
`hookPreTool`'s panic recovery or stdin reader.
