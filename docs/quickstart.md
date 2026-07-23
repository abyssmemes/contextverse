# Quickstart

## Solo (local context)

```bash
contextd init solo
cd <space-or-project>
contextd activate        # write entry points for detected AI tools
contextd status
contextd mcp serve       # stdio MCP for Claude / Cursor
```

Optional: seed from a community template — see [`contextverse-templates`](https://github.com/abyssmemes/contextverse-templates).

### Session-start plugins

`contextd activate` / `plugin install` detect AI clients (Claude Code, Cursor, Windsurf, Copilot, …) and wire each slot from embedded + community `client-integrations`. Ambiguous matches ask on a TTY; otherwise you get paste instructions.

Refresh community integrations:

```bash
contextd plugin refresh
```

### ChatGPT / closed web UIs

```bash
contextd export --format chatgpt
# → ~/contextverse-export/ for manual Knowledge upload
```

## Server (team host)

```bash
# Interactive setup UI (default)
contextd init server
# opens http://127.0.0.1:8743/setup

# Headless
contextd init server --noui --non-interactive \
  --admin admin --space team

contextd server start
contextd server status
contextd server health
```

Default listen: `127.0.0.1:8743`. Admin UI: `/ui/…`. API: `/api/v1/…`.

Create a user / token (after setup):

```bash
contextd auth user add alice --role contributor
# or login:
contextd auth login --user admin
```

See [Server](server.md) and [Auth & ACL](auth-acl.md).

## Client (sync to a server)

```bash
contextd init client --url https://context.example.com --space team
contextd pull
contextd activate
contextd push
```

## Service wrappers

| Platform | Unit |
|---|---|
| Linux systemd | `deploy/contextd.service` / `contextd server unit` |
| macOS launchd | `deploy/contextd.plist` |
