<div align="center">

# ContextVerse

**Context as a Service.**

Every AI tool wants to own your memory. ContextVerse gives it back to you.

One context for every AI tool — Claude, ChatGPT, Cursor, Copilot.
Install in 1 minute, configure in 2. Full control for your team or enterprise,
with a single source of truth.

**Self-hosted · Vendor-neutral · Open source**

[![ci](https://github.com/orkcom-tech/contextverse/actions/workflows/ci.yml/badge.svg)](https://github.com/orkcom-tech/contextverse/actions/workflows/ci.yml)
[![docs](https://github.com/orkcom-tech/contextverse/actions/workflows/docs.yml/badge.svg)](https://orkcom-tech.github.io/contextverse/)
[![license](https://img.shields.io/badge/license-BUSL--1.1-blue)](./LICENSE)

[Documentation](https://orkcom-tech.github.io/contextverse/) ·
[Install](#install) ·
[Quickstart](#quickstart) ·
[Templates](https://github.com/orkcom-tech/contextverse-templates)

</div>

> **Status: early development.** The commands, HTTP API and on-disk layout are not yet stable. Pin a release if you depend on them.

---

## The problem

Claude has its projects. ChatGPT has its memory. Cursor has its rules. They don't talk to each other, they don't sync, and when you switch tools you start from zero.

For a team it is worse. There is no way to onboard a developer into your AI workflow, no way to share context across the team, no way to enforce AI rules company-wide. Every developer builds their own silo, and the knowledge lives in Slack messages nobody reads.

## The solution

`contextd` is a single binary that holds one **context space** — plain Markdown you write and own — and delivers it into whatever entry point each AI tool actually reads.

You curate; the AI reads. Nothing is inferred, embedded, or stored behind your back.

```bash
contextd init                              # guided setup: solo, client, or server
cd ~/projects/api && contextd activate     # Claude, Cursor and Copilot now read the same space
contextd file history team/principles.md   # v4 — who changed the rules, and when
```

---

## What it looks like in practice

### Procedures your AI already has open

Your team's real knowledge isn't facts, it's procedures: which script to run, which one never to run, what to do when it fails. Today that lives in one senior engineer's head and gets re-explained to every AI, every session, by every developer.

Write it once, as a file you own:

```markdown
# team/deploy.md

## Deploying to production

Production deploys go through ArgoCD — never run `helm upgrade` directly.

    ./scripts/deploy.sh <service> <version>     # deploy
    ./scripts/rollback.sh <service>             # rollback

If the sync hangs >5 min, page #platform-oncall before retrying.
```

After `contextd activate`, Claude, Cursor and Copilot are all handed that runbook — with your script paths — at the start of every session, instead of each improvising a plausible-looking `helm` command.

> **Honest ceiling:** delivery is guaranteed, obedience is not. ContextVerse makes sure the model *has* your procedure; it cannot make a model obey it.

### Context that can't rot silently

Stale context is worse than no context — an AI confidently repeating a deploy process you replaced six months ago is a bug you cannot see. So freshness is not left to someone remembering:

```yaml
---
last-validated: 2026-07-26
stale-after: 30d
owner: platform-team
---
```

```bash
contextd freshness check          # what's stale, who owns it, since when
contextd freshness nag --server   # emit freshness.stale webhooks → Slack, Jira, PagerDuty
contextd freshness validate team/deploy.md
```

Your context becomes an auditable asset with an owner and a review cycle.

### Onboarding, in one command

```bash
contextd init client --url https://context.my-team.com --token cv-alice-xxxx
```

The space is cloned, the developer's identity is set, and their AI tools are wired.

### The same context on a second machine

`identity/` ships as `init-only`: seeded once from the template, then it belongs to that machine and is never pushed back. That default protects a **team** space, where one person's `me.md` must not land on everyone. For your own two machines there is no team copy to protect, so pick whichever of these fits:

| | How | When it fits |
|---|---|---|
| **Rebuild** | `contextd init solo` again, answer the questions | Two machines, different roles — a work laptop and a personal one |
| **Git** | `contextd backend set git --url …` | You already live in git and want the history to be the same history |
| **Your own server** | `contextd init server`, then point both machines at it | You want it automatic. Set `contextd space sync set <space> identity/ always` — on a personal server that is correct, on a shared one it is not |

The tool does not decide this for you, because the right answer depends on whether the second machine is *yours* or *someone else's*, and only you know that.

---

## What makes it different

Stated as our own capabilities, not as claims about anyone else's product:

| | |
|---|---|
| **Self-hosted on any infrastructure** | Your server, your storage backend — filesystem, git, S3, SQL. No vendor account required to run it. |
| **Path ACL, deny-by-default** | Explicit deny wins, most-specific path wins. Access is a policy, not a sharing toggle. |
| **Per-file version history** | Every file has `vN`, a body, and an author. Restore any version. |
| **Audit trail** | Every operation records actor, timestamp and target, hash-chained. |
| **Freshness with owners** | Context declares when it expires and who owns it; staleness raises a webhook. |
| **Vendor-neutral by construction** | The same space is delivered to Claude, Cursor, Copilot and ChatGPT. Switching tools costs nothing. |

---

## Install

<table>
<tr><th>macOS</th><th>Windows</th></tr>
<tr valign="top"><td>

```bash
brew tap orkcom-tech/tap
brew install orkcom-tech/tap/contextd
```

</td><td>

```powershell
scoop bucket add contextverse https://github.com/orkcom-tech/scoop-bucket
scoop install contextd
```

</td></tr>
</table>

**macOS / Linux — install script**

```bash
curl -fsSL https://raw.githubusercontent.com/orkcom-tech/contextverse/main/scripts/install.sh | bash
```

**Windows — install script**

```powershell
irm https://raw.githubusercontent.com/orkcom-tech/contextverse/main/scripts/install.ps1 | iex
```

**Go**

```bash
go install github.com/orkcom-tech/contextverse/cmd/contextd@latest
```

Also published as `.deb` / `.rpm` on [Releases](https://github.com/orkcom-tech/contextverse/releases). The tap and bucket are ours — not submitted to homebrew-core or Scoop extras. Details in [`scripts/README.md`](./scripts/README.md).

## Quickstart

The installer offers to run setup for you. To do it by hand:

```bash
contextd init
```

A guided wizard: pick a mode, and every choice is explained as you make it.

<table>
<tr><th>Mode</th><th>For</th><th>What it does</th></tr>
<tr><td><b>solo</b></td><td>One person, one machine</td><td>Local space at <code>~/.context</code>. No server, no account.</td></tr>
<tr><td><b>client</b></td><td>Joining a team</td><td>Syncs a space from a server you have a token for.</td></tr>
<tr><td><b>server</b></td><td>Hosting for a team</td><td>Spaces, users, path ACL, audit. Opens a setup page.</td></tr>
</table>

Then, in any project:

```bash
contextd activate     # write entry points for every detected AI tool
contextd status       # what is wired, and where
contextd tui          # browse and edit the space full-screen
```

Change your mind later with `contextd init --reconfigure`.

## How the context reaches your AI

`contextd` writes into each tool's own session-start slot. The slots are not equal, and we don't pretend they are:

| Client | Slot | Nature |
|---|---|---|
| **Claude Code** | `SessionStart` hook | **Live** — re-reads the space every session |
| **Cursor** | `.cursor/rules/*` | Snapshot, refreshed on `activate` |
| **Windsurf** | `.windsurfrules` | Snapshot |
| **GitHub Copilot** | `.github/copilot-instructions.md` | Snapshot |
| **opencode** | `AGENTS.md` (marked block) | Snapshot; your own content is preserved |
| **MCP clients** | `contextd mcp serve` | Live tools over stdio |
| **ChatGPT / web UIs** | — none — | `contextd export --format chatgpt`, manual upload |

Adding a new AI client is a PR to [`contextverse-templates`](https://github.com/orkcom-tech/contextverse-templates), not a binary release.

## Built for scripting

`contextd` is meant to be driven from CI, not just by hand.

```bash
contextd status --json | jq -r .mode
contextd file list --json
contextd space list --yaml        # server-side
```

Exit codes are meaningful, so a pipeline can tell "retry later" from "you called this wrong":

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | usage or local state error |
| `2` | network, auth or permission failure |
| `3` | compare-and-swap rejected — someone else wrote first |

Shell completions: `contextd completion bash|zsh|fish|powershell`.

## Three surfaces, one core

The **CLI is primary**. The terminal UI (`contextd tui`) and the server's web dashboard are presentations of the same operations — they never expose anything the CLI cannot do. Pick whichever is comfortable; the result and the permissions are identical.

## Under the hood

- **Context space model** — layered `identity / team / projects` with entry files and freshness metadata.
- **Derived link graph** — the map comes from links your documents already contain, so backlinks, orphans and broken links are facts about what you wrote rather than inferences about it. `space-index.md` and `team/space-map.md` are generated from it, and paths pointing into a project's checkout are checked against the real files.
- **Three surfaces over one listing** — the CLI, the TUI and the web console answer "what is in this space" from the same code. They used to have a copy each, which agreed only on spaces the current build had just created.
- **Pluggable storage backend** — local filesystem, git, S3/MinIO, SQL. The backend is a dumb blob store with compare-and-swap; the core owns versioning, conflict and ACL semantics.
- **Per-file versioning** — Vault KV v2-style: integer versions shown as `vN` in CLI, TUI and web UI, with soft-delete, undelete and destroy.
- **Pull-based sync** — clients pull on activate; pushes are batched against the space head with CAS.
- **Path-based access control** — Vault-style policies and capabilities, deny-by-default.
- **Stateless HA** — no bespoke clustering: N stateless nodes behind a load balancer over an HA-capable backend.

## Repository layout

```
cmd/contextd/     binary entrypoint
internal/         cli · tui · server · storage · syncclient · plugins · authz · audit · …
docs/             user documentation (MkDocs → GitHub Pages)
deploy/           systemd / launchd units, Docker and Helm templates
packaging/        scoop and winget manifest templates
scripts/          install.sh · install.ps1 · ci/
```

| Related repo | Role |
|---|---|
| [`contextverse-templates`](https://github.com/orkcom-tech/contextverse-templates) | Context-space and client-integration templates (Apache-2.0) |
| [`homebrew-tap`](https://github.com/orkcom-tech/homebrew-tap) | Homebrew tap |
| [`scoop-bucket`](https://github.com/orkcom-tech/scoop-bucket) | Scoop bucket |

## Building from source

```bash
make build          # → bin/contextd
go test ./...
./bin/contextd init
```

Needs a recent Go toolchain. No cgo, no external services for the default build.

## Documentation

Full user documentation: **<https://orkcom-tech.github.io/contextverse/>**

The source of truth is [`docs/`](./docs/) in this repository — edit it there.

```bash
pip install -r requirements-docs.txt
mkdocs serve
```

## Contributing

Issues and design discussion are welcome. See [`docs/contributing.md`](./docs/contributing.md). The project is early; a CLA/DCO lands when it formally accepts external code.

New AI client support does **not** need a code change — send a client-integration template to [`contextverse-templates`](https://github.com/orkcom-tech/contextverse-templates).


## Who builds this

ContextVerse is developed by **ORKCOM**. Copyright is held by Eduard Lugovtsov; ORKCOM is the project's home rather than a separate rights holder, and the licence names the person accordingly.

## License

[Business Source License 1.1](./LICENSE) — **source-available**. Read it, self-host it, modify it, run it in production. You may not offer it to third parties as a hosted or embedded service that competes with ContextVerse. Each released version converts to **Apache-2.0** four years after its release.

Templates are Apache-2.0. The managed ContextVerse Cloud is a separate, proprietary product.
