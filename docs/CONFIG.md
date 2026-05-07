# Configuration

godx-arbiter reads configuration from three layers, evaluated in
**most-specific-wins** order:

1. **Per-project**: `<project>/.arbiter/rules.md` + `policy.yaml` +
   `skills/` — see [RULES_SPEC.md](RULES_SPEC.md) and
   [POLICY_SPEC.md](POLICY_SPEC.md).
2. **Per-user**: `$GODX_ARBITER_HOME/config.yaml` (or
   `~/.config/godx-arbiter/config.yaml`) — global defaults.
3. **Environment variables**: override anything from layers 1 and 2 on
   a per-process basis.

Keys + secrets are managed separately by [`arbiter auth`](CLI.md#arbiter-auth)
in the OS keychain.

---

## Global config (`config.yaml`)

```yaml
# proxy server defaults
proxy:
  addr: ":7777"
  anthropic: https://api.anthropic.com   # override upstream URL (rarely needed)
  openai:    https://api.openai.com
  gemini:    https://generativelanguage.googleapis.com

# fallback rules.md used when cwd has no .arbiter/
fallback_rules: ./global-rules.md

# per-CLI configuration (mirrors docs/MULTI_CLI.md)
clis:
  claude-code:
    mode: hook                # hook | proxy | hybrid
    hooks:
      pre_tool: true
      notification: true
      stop: true
    mcp_register: true

  codex:
    mode: proxy
    proxy_endpoint: http://localhost:7777/v1
    api_key_env: ARBITER_OPENAI_KEY    # arbiter holds the real key

  gemini:
    mode: proxy
    proxy_endpoint: http://localhost:7777/v1beta
    api_key_env: ARBITER_GEMINI_KEY

# default notification preferences (overridden by per-project rules.md)
notify:
  channels: [telegram, desktop]
  quiet_hours: "22:00-07:00"
  dedup_secs: 60
```

The file is optional; an empty / missing config produces zero-valued
defaults that match the hard-coded behavior described elsewhere in the
docs. `arbiter init` does **not** write this file today — edit it by
hand or programmatically via Go (`config.LoadGlobal` /
`config.GlobalConfig.Save`).

### Resolution

| Path | Source |
|---|---|
| `$GODX_ARBITER_HOME/config.yaml` | env override (also used in tests) |
| `$XDG_CONFIG_HOME/godx-arbiter/config.yaml` | XDG-compliant systems |
| `~/.config/godx-arbiter/config.yaml` | default |

---

## Per-project config (`.arbiter/`)

```
<project>/.arbiter/
├── rules.md       # free-form Markdown rules + YAML front matter
├── policy.yaml    # regex fast-path
└── skills/        # optional Markdown skills (see SKILLS.md)
```

The agent walks up from the current working directory looking for the
first `.arbiter/`. If absent, it uses `fallback_rules` from the global
config (or built-in defaults).

See [RULES_SPEC.md](RULES_SPEC.md), [POLICY_SPEC.md](POLICY_SPEC.md),
and [SKILLS.md](SKILLS.md) for content specs.

---

## Environment variables

| Var | Default | Purpose |
|---|---|---|
| `ANTHROPIC_API_KEY` | — | Slow-path agent's API key. Honored by the SDK directly. Use [`arbiter auth set anthropic`](CLI.md#arbiter-auth) for keychain storage instead. |
| `OPENAI_API_KEY` | — | Same, for OpenAI in proxy mode. |
| `GOOGLE_API_KEY` | — | Same, for Gemini. |
| `GODX_ARBITER_HOME` | `~/.config/godx-arbiter` | Override the per-user data root (config.yaml, credentials, events.jsonl, usage.jsonl, skills/). |
| `GODX_ARBITER_LOG_PATH` | `$GODX_ARBITER_HOME/events.jsonl` | Override the eventlog path specifically (e.g. for pytest harnesses). |
| `GODX_ARBITER_USAGE_PATH` | `$GODX_ARBITER_HOME/usage.jsonl` | Same for the usage ledger. |
| `GODX_ARBITER_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. |
| `GODX_ARBITER_DISABLED` | unset | If `1`, every decision short-circuits to allow with `path: kill-switch`. Emergency override; doesn't require restarting Claude Code. |
| `GODX_ARBITER_TELEGRAM_TOKEN` | — | Telegram bot token (also via `arbiter auth set telegram`). |
| `GODX_ARBITER_TELEGRAM_CHAT_ID` | — | Telegram chat id for escalations. |
| `GODX_ARBITER_WEBHOOK_URL` | — | URL for the webhook notify channel. |
| `GODX_ARBITER_WEBHOOK_TOKEN` | — | Optional bearer for the webhook channel. |
| `XDG_CONFIG_HOME` / `XDG_DATA_HOME` | — | Honored when `GODX_ARBITER_HOME` isn't set. |

---

## Settings.json (Claude Code's file)

`arbiter init` merges into this:

```json
{
  "hooks": {
    "PreToolUse":   [{"hooks": [{"type": "command", "command": "arbiter hook pretool"}]}],
    "PostToolUse":  [{"hooks": [{"type": "command", "command": "arbiter hook posttool"}]}],
    "Notification": [{"hooks": [{"type": "command", "command": "arbiter hook notification"}]}],
    "Stop":         [{"hooks": [{"type": "command", "command": "arbiter hook stop"}]}]
  },
  "mcpServers": {
    "godx-arbiter": { "command": "arbiter", "args": ["mcp"] }
  }
}
```

Existing entries are preserved (we look for `arbiter hook ` substring
in the command to avoid duplicating our own block on re-runs). A
timestamped `settings.json.arbiter-backup-<ts>` is always written
before mutation. `arbiter uninstall` reverses this — see
[CLI.md](CLI.md#arbiter-uninstall).

---

## Examples

### Solo developer, default-balanced project

```yaml
# ~/.config/godx-arbiter/config.yaml
proxy:
  addr: ":7777"
notify:
  channels: [desktop, log]
  quiet_hours: "22:00-07:00"
```

```bash
arbiter auth set anthropic
arbiter init                       # in each project
arbiter doctor
```

### Team with Telegram approvals

```yaml
notify:
  channels: [telegram, desktop, log]
  quiet_hours: "22:00-07:00"
  dedup_secs: 30
```

```bash
arbiter auth set telegram                    # bot token
export GODX_ARBITER_TELEGRAM_CHAT_ID=...     # group/channel id
arbiter doctor --notify-test                 # confirm message arrives
```

### CI / sandbox container (fail-open + minimal noise)

```yaml
# .arbiter/rules.md
---
on_error: approve
on_timeout: approve
notify_channels: [log]
---

## Auto-approve
- Everything outside the deny list

## Deny
- rm -rf / (root)
```

`GODX_ARBITER_DISABLED=1` is also valid for "I just want this off
right now."
