# Cursor — client integration

## Client

[Cursor](https://cursor.com). Session-start / always-on slot: project `.cursor/rules/*.mdc` (vendor rules injection).

## Mechanism

`rules-slot` — Cursor does not expose a command hook. The strongest available slot is a project rule file that the IDE injects into context.

## Honest ceiling

**Static snapshot** regenerated on `contextd activate` (and `plugin install`). Not live every keystroke — refresh requires activate/pull. Delivery into the vendor slot is deterministic; **obedience is not guaranteed**.

## Idempotent merge

Writes/replaces the single `contextd`-owned file `{{cwd}}/.cursor/rules/contextverse.mdc`. Does not edit other rules.

## Detection

Any of: directory `~/.cursor/` exists, or `cursor` on `PATH`.
