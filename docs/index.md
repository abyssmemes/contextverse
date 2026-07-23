# ContextVerse

**Portable, vendor-neutral context for AI.** Own your context — use any AI, switch freely, stay in sync.

This site is the **user documentation** for the open-source [`contextd`](https://github.com/abyssmemes/contextverse) binary (CLI + server). It lives in the same git repository under [`docs/`](https://github.com/abyssmemes/contextverse/tree/main/docs) and is published with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/) on GitHub Pages.

## Why

Every AI tool keeps context in its own silo. Switch tools and you start over; use several at once and they drift. ContextVerse inverts that: **one curated context space**, delivered into whatever entry point each tool reads (Claude Code, Cursor, Copilot, ChatGPT export, MCP, …).

## Three modes, one binary

| Mode | Role |
|---|---|
| **solo** | Local context on your machine — no server |
| **server** | Host context for a team (pluggable storage, ACL, sync) |
| **client** | Pull/push against a server |

## Start here

1. [Install](install.md)
2. [Quickstart](quickstart.md)
3. [CLI](cli.md) · [Server](server.md) · [Auth & ACL](auth-acl.md)

## Status

Early development. Commands and on-disk layout may still change. Track releases on [GitHub Releases](https://github.com/abyssmemes/contextverse/releases).

## License

[BUSL-1.1](https://github.com/abyssmemes/contextverse/blob/main/LICENSE) (source-available). Self-host and use in production; do not offer a competing hosted service. Each version converts to Apache-2.0 four years after release.
