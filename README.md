# ContextVerse

**Portable, vendor-neutral context for AI. Own your context — use any AI, switch freely, stay in sync.**

This is the open-source ContextVerse repo: the **`contextd`** CLI/server binary **and** the install scripts that get it onto a machine. One curated, versioned context you control — delivered to Claude, Cursor, Copilot, ChatGPT, and others. Your context lives with you, not inside one vendor.

> Status: **early development.** APIs, on-disk layout, and commands are not yet stable.

## Why

Every AI tool keeps your context in its own silo. Switch tools and you start over; use several at once and they drift out of sync. ContextVerse inverts that: **one curated context**, generated into whatever entry point each tool reads. Vendor neutrality is the point — it's the thing AI vendors won't build for you.

## Three modes, one binary

- **solo** — standalone local context on your machine. No server, no account.
- **server** — host context for a team over a pluggable storage backend, with roles and sync.
- **client** — pull/push context to and from a server.

## Install

**macOS / Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.ps1 | iex
```

**From source / Go**

```bash
go install github.com/abyssmemes/contextverse/cmd/contextd@latest
```

While the repo is private, set `GITHUB_TOKEN` or run `gh auth login` first. Details: [`scripts/README.md`](./scripts/README.md).

Package managers (Homebrew, apt, scoop, winget) come later.

## Quickstart (solo)

```bash
contextd init solo      # create & configure a local context space
contextd activate        # generate entry points (CLAUDE.md, .cursor/rules, …)
contextd status          # see what's active and fresh
```

Optional: seed from a community template — see [`contextverse-templates`](https://github.com/abyssmemes/contextverse-templates).

## Key ideas

- **Context space model** — layered `identity / team / projects` with entry files and freshness/importance metadata.
- **Entry-point generation** — one space compiles to `CLAUDE.md`, `.cursor/rules/*`, MCP, and more.
- **Pluggable storage backend** — local FS, S3, or git remote now; SQL/NoSQL later.
- **Pull-based sync** — clients pull on activate; optional live pings over SSE.
- **Vault-style access control** (server) — deny-by-default, explicit-deny-wins, most-specific-path.

## Repository layout

```
cmd/contextd/          # binary entrypoint
internal/
  cli/                 # Cobra commands
  config/              # YAML config + mode detection
  space/               # space model + embedded templates
  entrypoint/          # CLAUDE.md + .cursor/rules generation
  logx/                # structured logging
scripts/               # install.sh / install.ps1 (Phase 0 install path)
```

Related repos:

| Repo | Role |
|---|---|
| [`contextverse-templates`](https://github.com/abyssmemes/contextverse-templates) | Community context-space templates |
| [`contextverse-cloud`](https://github.com/abyssmemes/contextverse-cloud) | Managed cloud (proprietary, private) |

## Building from source

```bash
make build                 # → bin/contextd
./bin/contextd version
./bin/contextd init solo   # interactive; or --non-interactive --name …
cd <project> && ./bin/contextd activate
```

Requires a recent Go toolchain (`go test ./...` for checks).

## Documentation

Public docs will ship as a **GitHub Wiki** on this repo alongside the first tagged release. Design notes currently live in the project knowledge base.

## Contributing

Early-stage — issues and design discussion welcome. Contribution guidelines and a CLA/DCO land with the first releases that accept external PRs.

## License

[Business Source License 1.1](./LICENSE) (BUSL-1.1) — **source-available**. You may read, self-host, modify, and use ContextVerse in production. You may **not** offer it to third parties as a hosted or embedded service that competes with ContextVerse. Each released version automatically converts to **Apache-2.0** four years after its release.

The managed ContextVerse Cloud is a separate, proprietary product.
