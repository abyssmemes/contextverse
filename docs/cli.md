# CLI

`contextd` is **one binary**. The mode comes from your config (`solo` | `client` | `server`), not from which subcommand exists — `contextd status` works the same everywhere, and `contextd pull` tells you plainly if you are not a client.

The CLI is the **primary surface**. The [TUI](#terminal-ui) and the server's web dashboard present the same operations; neither can do anything the CLI cannot.

## Command groups

`contextd --help` is grouped by the job you are doing:

### Set up and inspect

```bash
contextd init                    # guided setup — mode, identity, template, backend, AI tools
contextd init --reconfigure      # change an existing installation
contextd init solo|client|server # non-interactive paths for scripts and CI
contextd activate                # write entry points for detected AI tools
contextd status                  # mode, identity, backend, what's missing
contextd version
```

### Work on your context space

```bash
contextd file list                       # tracked files with their versions
contextd file edit <path>                # open in $EDITOR, save as a new version
contextd file put <path> --from FILE|-   # write from a file or stdin
contextd file get <path> [-v N]          # print the body of any version
contextd file history <path>             # who changed it, and when
contextd file revert <path> -v N         # bring an old version back as current
contextd file undelete <path>            # restore a soft-deleted file
contextd file destroy <path> -v N        # permanently remove one version

contextd space seed --template <name>    # re-seed your own space
contextd space list|show|create|delete   # a server's spaces (see Server)

contextd index update                    # regenerate space-index.md
contextd template list                   # browse the template catalog
contextd freshness check|nag|validate    # stale context and its owners
```

### Sync and storage

```bash
contextd pull [--check]          # fetch changes from the server
contextd push [--check]          # publish yours
contextd daemon start|stop|status
contextd backend show|list|set|test|migrate
contextd history snapshot|list|show|restore
```

### Deliver context to AI tools

```bash
contextd mcp serve                       # MCP server over stdio
contextd plugin list|install|refresh     # client integrations
contextd context inject                  # the payload a session-start hook emits
contextd export --format chatgpt         # for closed web UIs
```

### Administer a server

```bash
contextd server start|stop|status|health|logs|unit
contextd server tls gen                        # lab self-signed
contextd server tls acme enable|status         # Let's Encrypt

contextd user add|list|role|policy|disable|enable|remove|reset-token
contextd auth token create|list|revoke
contextd auth login|password-set

contextd policy list|show|write|test
contextd acl allow|deny|list|unset
contextd audit list|export|stats|verify
contextd webhooks add|list|test|delete|dead-letter
```

### Interfaces and help

```bash
contextd tui [--server]
contextd completion bash|zsh|fish|powershell
```

## Global flags

| Flag | Meaning |
|---|---|
| `--dir` | Space root (solo/client). Default `~/.context` |
| `--server-dir` | Server data directory |
| `--json` / `--yaml` | Structured output, where supported |
| `--debug` | Debug logs on stderr |

## Output for scripts

`--json` and `--yaml` are supported by `status`, `file list`, `file history`, `user list`, `space list`, `space show`, `pull` and `push`. Passing both is an error.

```bash
contextd status --json | jq -r .mode
contextd file list --json | jq -r '.[] | select(.version == "v1") | .path'
contextd space list --yaml
```

They are deliberately **not** supported by `init`, `tui`, `completion`, `mcp serve`, `daemon` or `file get` — those either have no structural result, or (in `file get`'s case) emit the file's own bytes, and wrapping them would break every existing pipe.

## Exit codes

| Code | Meaning | Typical cause |
|---|---|---|
| `0` | Success | |
| `1` | Usage or local state error | Wrong path, missing flag, not in the right mode |
| `2` | Network, auth or permission failure | Server unreachable, expired token, denied by policy |
| `3` | Compare-and-swap rejected | Someone else wrote first — pull and retry |

```bash
contextd push || case $? in
  2) echo "server unreachable or token expired" ;;
  3) contextd pull && contextd push ;;
esac
```

## File versions

Every file carries an integer version, Vault KV v2-style: the API's `ETag: "3"` is displayed as **`v3`** in the CLI, TUI and web UI alike. Writes are compare-and-swap against the version you read, so a concurrent change is reported rather than silently overwritten.

```bash
contextd file history team/deploy.md
# path=team/deploy.md current=v4
#   v4  2026-07-26T10:12:03Z  1841 bytes current
#   v3  2026-07-19T08:41:55Z  1620 bytes
```

## Terminal UI

```bash
contextd tui            # your own space
contextd tui --server   # server admin (uses --server-dir)
```

Tabs: Space · Projects · Files · Plugins · Output · Help. On the Files tab, `enter` opens the file in your editor, `v` previews, `V` shows version history, `R` restores. Saving writes a new version through the same path `contextd file edit` uses.

Press `?` for the full key list. It is grouped the same way this page is.

## Completions

```bash
# zsh
contextd completion zsh > "${fpath[1]}/_contextd" && compinit

# bash
contextd completion bash | sudo tee /etc/bash_completion.d/contextd

# fish
contextd completion fish > ~/.config/fish/completions/contextd.fish
```

## More

- [Quickstart](quickstart.md) · [Install](install.md)
- [Server](server.md) · [Auth & ACL](auth-acl.md)
- Source: [github.com/abyssmemes/contextverse](https://github.com/abyssmemes/contextverse)
