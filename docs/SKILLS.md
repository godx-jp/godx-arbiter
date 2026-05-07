# Skills system

A **skill** is a reusable Markdown chunk that the slow-path agent can
load into its system prompt on demand. Skills let you keep `rules.md`
short while still pulling in detailed context (allowlists, checklists,
naming conventions) for specific decisions.

## Including a skill

In `rules.md`, add a directive on its own line:

```markdown
@include skill:safe-bash-allowlist
@include skill:review-before-merge
```

The directive is matched anywhere in the body. Order is preserved;
duplicate names are deduped. Missing skills are skipped with a
warning to stderr — the decision continues with whatever resolved.

## Resolution order

For each `@include skill:<name>`, the agent searches:

1. `<project>/.arbiter/skills/<name>.md`
2. `$GODX_ARBITER_HOME/skills/<name>.md`
3. `$XDG_CONFIG_HOME/godx-arbiter/skills/<name>.md`
4. `~/.config/godx-arbiter/skills/<name>.md`
5. The built-in library (see below)

First hit wins, so projects can override globals, and globals override
the built-ins.

## Built-in library

| Name | What it covers |
|---|---|
| `safe-bash-allowlist` | Read-only Bash commands the agent may approve without further analysis |
| `review-before-merge` | Pre-merge checklist (test coverage, secrets, hardcoding, file count, migration discipline) |
| `test-before-deploy` | Verify tests + clean working tree before any deploy-touching action |
| `migration-discipline` | Append-only migrations; deny modify/rename of older files |
| `secret-scanning` | Patterns the agent should treat as secret-shaped and deny outright |

The bodies live in [`internal/skills/skills.go`](https://github.com/godx-team/godx-arbiter/blob/main/internal/skills/skills.go).
Override any of them by writing your own file at the same name in
your project's `.arbiter/skills/`.

## Writing a skill

```markdown
# Skill: <name> (auto-prepended)

A short orienting paragraph — what this skill is for and when the
agent should consult it.

## Signals this skill recognizes

- Bullet points the agent can match against
- ...

## Decisions this skill prescribes

- "If <signal>, deny because <reason>."
- "If <signal>, approve because <reason>."
```

Tips:

- **Token budget is real**, even with prompt caching. A 500-line skill
  costs ~1500 tokens on every uncached call. Keep skills focused.
- **Prefer positive prescriptions** over negative ones — the agent
  follows "approve when …" more reliably than "don't deny when ….".
- **Avoid contradicting `rules.md`**. The agent will surface conflicts
  by escalating; surprising users.
- **Don't put secrets in skills**. The agent may quote them back to
  itself in tool calls, and skills can be redistributed.

## Example

`<project>/.arbiter/skills/sandbox-tooling.md`:

```markdown
# Sandbox tooling

Code under `backend/internal/sandbox/` is being refactored by Duong
(PR #317 series, branch `feat/admin-delegate-to-sandbox-service`).

## Prescription

- Until that branch merges, **escalate any edit** to files in this
  directory — even if it would otherwise auto-approve.
- The escalation should call out the in-flight branch by name.

## Auto-clear

Remove this skill (or the `@include` line) once #317 lands.
```

In `<project>/.arbiter/rules.md`:

```markdown
@include skill:sandbox-tooling
```

The agent now treats the directive as if those rules were inlined,
without `rules.md` itself ballooning.
