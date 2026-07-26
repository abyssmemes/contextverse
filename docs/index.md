# ContextVerse

**Context as a Service.** Every AI tool wants to own your memory. ContextVerse gives it back to you.

One context for every AI tool — Claude, ChatGPT, Cursor, Copilot. Self-hosted, vendor-neutral, open source.

!!! warning "Early development"
    Commands, the HTTP API and the on-disk layout are not yet stable. Pin a release if you depend on them — see [GitHub Releases](https://github.com/abyssmemes/contextverse/releases).

## The problem

Claude has its projects. ChatGPT has its memory. Cursor has its rules. They don't talk to each other, they don't sync, and when you switch tools you start from zero.

For a team it is worse: no way to onboard a developer into your AI workflow, no way to share context, no way to enforce AI rules company-wide. Every developer builds their own silo.

## What `contextd` does

It holds one **context space** — plain Markdown you write and own — and delivers it into whatever entry point each AI tool actually reads.

You curate; the AI reads. Nothing is inferred, embedded, or stored behind your back.

```bash
contextd init                              # guided setup
cd ~/projects/api && contextd activate     # every detected AI tool now reads the same space
contextd file history team/principles.md   # v4 — who changed the rules, and when
```

## Three modes, one binary

| Mode | For | What you get |
|---|---|---|
| **solo** | One person, one machine | A local space at `~/.context`. No server, no account. |
| **client** | Joining a team | Pull/push against a server you have a token for. |
| **server** | Hosting for a team | Spaces, users, path ACL, audit log, webhooks. |

The mode lives in your config, not in which subcommand exists — one binary does all three.

## Start here

1. **[Install](install.md)** — Homebrew, Scoop, install script, `go install`, `.deb` / `.rpm`
2. **[Quickstart](quickstart.md)** — from nothing to a wired AI tool
3. **[CLI](cli.md)** — the command surface, output formats, exit codes

Then, as needed: **[Server](server.md)** · **[Auth & ACL](auth-acl.md)** · **[Deploy](deploy.md)**

## What makes it different

- **Self-hosted on any infrastructure** — your server, your storage backend (filesystem, git, S3, SQL). No vendor account required to run it.
- **Path ACL, deny-by-default** — explicit deny wins, most-specific path wins. Access is a policy, not a sharing toggle.
- **Per-file version history** — every file has `vN` and an author. Restore any version.
- **Audit trail** — every operation records actor, timestamp and target, hash-chained.
- **Freshness with owners** — context declares when it expires and who owns it; staleness raises a webhook.
- **Vendor-neutral by construction** — the same space feeds Claude, Cursor, Copilot and ChatGPT. Switching tools costs nothing.

## An honest limit, stated once

`contextd` guarantees **delivery**: your context reaches every wired tool at session start. It cannot guarantee **obedience** — a language model can still ignore an instruction it was handed. Anything claiming otherwise is selling you something.

## License

[BUSL-1.1](https://github.com/abyssmemes/contextverse/blob/main/LICENSE), source-available: self-host, modify and run it in production; don't offer a competing hosted service. Each version converts to Apache-2.0 four years after release. Templates are Apache-2.0.
