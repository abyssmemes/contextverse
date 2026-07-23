# CLI

`contextd` is **one binary**. Mode comes from config (`solo` | `client` | `server`), not from which subcommand exists.

The CLI is the primary surface. Web UI and TUI wrap the same capabilities (admin: spaces, users/tokens, freshness nag, backends, audit, webhooks; file `vN` View/Restore).

## Global flags

| Flag | Meaning |
|---|---|
| `--dir` | Space root (client/solo), default `~/.context` |
| `--server-dir` | Server data dir |
| `--json` / `--yaml` | Structured output |
| `--debug` | Debug logs on stderr |

## Common commands

```bash
contextd version
contextd status
contextd init solo|server|client
contextd activate
contextd pull | push
contextd mcp serve
contextd export --format chatgpt
contextd completion zsh|bash|fish|powershell

# Server host
contextd server start|stop|status|health
contextd server tls gen
contextd server tls acme enable|status

# Auth / ACL (server)
contextd auth …
contextd acl …
contextd policy …
```

## File versions

Per-file history uses integer CAS versions (Vault KV v2-style). API `ETag: "3"` displays as **`v3`** in CLI, Web UI, and TUI. Soft-delete / undelete / destroy are supported on the server data plane.

## Completions

```bash
# zsh example
contextd completion zsh > "${fpath[1]}/_contextd"
```

## More

- [Install](install.md) · [Quickstart](quickstart.md)
- Upstream README: [github.com/abyssmemes/contextverse](https://github.com/abyssmemes/contextverse)
