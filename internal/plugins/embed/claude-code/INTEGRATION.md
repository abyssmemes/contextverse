# Claude Code — client integration

## Client

[Claude Code](https://docs.anthropic.com/en/docs/claude-code) (CLI). Session-start slot: `hooks.SessionStart` in `~/.claude/settings.json`. Claude runs configured commands at session start and can inject their stdout into context.

## Mechanism

`command-hook` — strongest slot this client exposes. A hook runs `contextd context inject --format claude-hook` every session.

## Honest ceiling

**Live delivery** of the entry set into the session (always-fresh from the active ContextVerse space). This is **delivery, not obedience** — the model can still ignore the injected text. No other AI client in the well-known set has an equivalent live command hook today.

## Idempotent merge

`contextd` merges a `SessionStart` hook whose `command` equals the integration `command` field. Re-running install replaces that one hook entry and leaves other hooks/settings untouched.

## Detection

Any of: directory `~/.claude/` exists, or `claude` on `PATH`.
