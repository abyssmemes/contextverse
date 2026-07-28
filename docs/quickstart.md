# Quickstart

From a fresh install to an AI tool reading your context.

!!! tip "The installer offers to do this for you"
    `install.sh` and `install.ps1` end by offering to run `contextd init`. Everything below is that same wizard, done by hand.

## 1. Pick a mode

```bash
contextd init
```

A guided setup: choose a mode, and every option is explained as you select it.

| Mode | For | Needs |
|---|---|---|
| **solo** | One person, one machine | Nothing |
| **client** | Joining a team | A server URL and an API token from its admin |
| **server** | Hosting for a team | A machine others can reach |

The wizard asks for your identity, offers a starting template from the catalog, lets you pick a storage backend, and wires the AI tools it finds. It finishes by listing every file it created and what each one is for.

Changed your mind later:

```bash
contextd init --reconfigure
```

For CI and scripts, the subcommands take flags instead:

```bash
contextd init solo --non-interactive --name "Alice" --role "Backend developer"
```

## 2. Wire your AI tools

In any project directory:

```bash
cd ~/projects/api
contextd activate
```

This writes each detected tool's session-start file — `CLAUDE.md`, `.cursor/rules/contextverse.mdc`, `.github/copilot-instructions.md`, `AGENTS.md` and so on — pointing at your space.

```bash
contextd plugin list      # what is detected, and how
contextd plugin install   # (re)wire specific clients
contextd plugin refresh   # fetch community integrations
```

See [how delivery works per client](#how-context-reaches-each-tool) below.

## 3. Write some context

The space is ordinary Markdown. Edit it however you like — `contextd` also has an editor that records a version each time you save:

```bash
contextd file edit identity/me.md      # opens $EDITOR, saves a new vN
contextd file list                     # every tracked file with its version
contextd file history identity/me.md   # who changed it, and when
contextd file get identity/me.md -v 2  # read an older version
```

Prefer a full-screen view:

```bash
contextd tui
```

## 4. Check your work

```bash
contextd status
```

## How context reaches each tool

The slots are not equal, and `contextd` does not pretend they are:

| Client | Slot | Nature |
|---|---|---|
| **Claude Code** | `SessionStart` hook in `settings.json` | **Live** — re-reads the space every session |
| **Cursor** | `.cursor/rules/contextverse.mdc` | Snapshot, refreshed on `activate` |
| **Windsurf** | `.windsurfrules` | Snapshot |
| **GitHub Copilot** | `.github/copilot-instructions.md` | Snapshot |
| **opencode** | `AGENTS.md`, marked block | Snapshot; your own content in that file is preserved |
| **MCP clients** | `contextd mcp serve` (stdio) | Live tools |

### ChatGPT and other closed web UIs

They expose no slot, so delivery is manual:

```bash
contextd export --format chatgpt
# → ~/contextverse-export/ , upload to a Project's Knowledge
```

### Nothing detected?

`contextd` asks which tools you actually use and wires those. It never guesses silently.

---

## Server quickstart

```bash
contextd init server
# opens the setup page at http://127.0.0.1:8743/setup
```

Headless or over SSH:

```bash
contextd init server --noui --non-interactive \
  --data-dir /srv/contextverse --space team --admin admin
```

The bootstrap admin token is printed **once** and also written to `<data-dir>/auth/bootstrap_admin.token`. It expires on its own — copy it, delete that file, then issue a long-lived one.

```bash
contextd server start --server-dir /srv/contextverse
contextd server status
contextd server health
```

Add a teammate:

```bash
contextd user add alice
contextd user role alice contributor
contextd user reset-token alice        # prints a token once — give it to Alice
```

Manage its spaces:

```bash
contextd space list
contextd space create design --template solo-default
contextd space show design
```

More in [Server](server.md) and [Auth & ACL](auth-acl.md).

## Client quickstart

Alice, with the URL and the token she was given:

```bash
contextd init client \
  --url https://context.example.com \
  --token cv-alice-xxxx \
  --space team
```

Or just `contextd init` and pick **client** — the wizard verifies the token before writing anything, and lets her choose from the spaces it can actually see.

Then:

```bash
contextd pull          # get the latest
contextd activate      # wire her AI tools
contextd push          # publish her changes
contextd daemon start    # optional: poll and pull in the background
contextd daemon install  # or have it start at login
```

## Looking around your own space

Once there is context in it, these are the everyday commands:

```bash
contextd search "deploy"     # find text — paths and contents
contextd graph               # the map your documents already describe
contextd graph --broken      # links that point at nothing
contextd freshness check     # what has gone stale, and who owns it
contextd tui                 # the same, in a terminal UI
contextd ui                  # or in a browser, on demand — Ctrl-C to stop
```

## Where to go next

- [CLI reference](cli.md) — every command, `--json` output, exit codes
- [Server](server.md) — configuration, TLS, webhooks, audit
- [Auth & ACL](auth-acl.md) — policies and path rules
- [Deploy](deploy.md) — Docker, Helm, systemd, launchd
