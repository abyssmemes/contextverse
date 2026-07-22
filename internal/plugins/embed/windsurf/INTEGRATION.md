# Windsurf — client integration

## Client

[Windsurf](https://codeium.com/windsurf). Session-start / always-on slot: project `.windsurfrules`.

## Mechanism

`rules-slot` — no command hook. Strongest available slot is a project rules file.

## Honest ceiling

**Static snapshot** regenerated on `contextd activate` / `plugin install`. Delivery into the vendor slot is deterministic; **obedience is not guaranteed**.

## Detection

Any of: `~/.codeium/`, `~/.windsurf/`, or `windsurf` on `PATH`.
