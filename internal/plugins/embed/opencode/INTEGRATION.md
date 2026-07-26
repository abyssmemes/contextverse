# opencode — client integration

## Client

[opencode](https://github.com/sst/opencode). Terminal AI coding agent. Instructions slot: `AGENTS.md` in the project root.

## Mechanism

`instructions-slot` with `merge: marked-block`.

`AGENTS.md` is **not ours**. It is usually hand-written, and several other agents read the same file, so contextd owns only a delimited region:

```
<!-- >>> contextverse >>> -->
…generated entry-set pointer…
<!-- <<< contextverse <<< -->
```

Everything outside the markers is preserved across `activate` and `plugin install`. A file with a begin marker but no end marker is refused rather than repaired — guessing where the block ended would eat the user's text.

This is the one embedded integration that shares its target. Cursor, Windsurf and Copilot write dedicated files and use `replace-file`.

## Honest ceiling

**Static snapshot** refreshed on activate/plugin install. Not a live hook. Delivery ≠ obedience.

## Detection

Any of: `opencode` on `PATH`, `~/.config/opencode/`, or `~/.local/share/opencode/`.
