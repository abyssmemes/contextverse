# Concepts

Five ideas explain the whole product. Read this once and the rest of the documentation stops being a list of commands.

## The context space

A **context space** is a directory of Markdown files that describes you, your team and your work. It is the thing every AI tool reads.

```
~/.context/
├── context-entry.md        the front door — tells an AI where to go next
├── space-index.md          the map of everything in the space
├── decisions.md            choices already made, not to be re-litigated
├── identity/
│   └── me.md               who the AI is talking to
├── team/
│   ├── principles.md       how you work — rules the AI should follow
│   ├── skill-map.md        what you can call on
│   └── space-map.md        navigation for larger spaces
└── projects/
    └── <project>/          one folder per project, with its own context
```

Three properties matter:

**It is ordinary Markdown.** You open it in your editor, read it, diff it, put it in git. There is no database and no proprietary format.

**You write it.** Nothing is inferred from your conversations, embedded into vectors, or stored behind your back. The tool does not decide what is relevant — you do.

**It is layered.** `identity` rarely changes, `team` changes when the team's rules change, `projects` changes constantly. That separation is what makes it possible to share the middle layer with colleagues without sharing the other two.

## Entry points

The problem is mundane: Claude Code reads `CLAUDE.md` from your working directory, Cursor reads `.cursor/rules/`, Copilot reads `.github/copilot-instructions.md`. None of them read `~/.context/`.

So `contextd activate` writes a small pointer file into each of those slots:

```bash
cd ~/projects/api
contextd activate
```

They are generated, not hand-maintained. Change the space, run `activate`, and every tool sees the change.

!!! info "Delivery is guaranteed; obedience is not"
    `contextd` makes sure the model *has* your context at the start of a session. It cannot make a model follow it — that is a property of the model, not of any tool. Only clients with a real command hook (Claude Code today) get live, always-fresh delivery; the rest get a snapshot refreshed on `activate`.

## Versions

Every write through `contextd` creates a new version of that file, numbered from 1 and displayed as `v3` everywhere — CLI, terminal UI and web console.

```bash
contextd file history team/principles.md
contextd file get team/principles.md -v 2
contextd file revert team/principles.md -v 2
```

Versions are not snapshots of the whole space; each file has its own line of history, in the style of Vault's KV v2. Deleting is soft by default (`undelete` brings it back); `destroy` is the irreversible one.

This is why the storage backend is deliberately dumb: it stores blobs and offers compare-and-swap, and `contextd` owns versioning, conflict resolution and access control on top. Swapping the filesystem for S3 changes where bytes live, nothing else.

## Sync

A **server** holds spaces for a team. Clients pull from it and push to it — there is no continuous replication and no merge algorithm.

```bash
contextd pull      # bring the server's changes down
contextd push      # publish yours
```

Pushes are compare-and-swap against the space head: if someone else pushed since your last sync, yours is rejected with exit code `3` and you pull first. That is deliberately boring — silent three-way merges of prose produce documents nobody wrote.

**Selective sync** decides what travels. Each space declares rules per path:

| Mode | Meaning |
|---|---|
| `always` | Synced in both directions |
| `init-only` | Copied once when you join, then yours alone — in both directions, so it is never pushed back up either. This is how `identity/` stays personal |

The modes are per space, not baked in. `identity/ init-only` is the right default for a team; on a server syncing your own two machines it is the wrong one, and you change it:

```bash
contextd space sync set my-space identity/ always
```
| `never` | Local only |

## Access control

The server enforces **path ACL**, in the shape Vault users will recognise:

- **Deny by default.** No rule means no access.
- **Explicit deny wins** over any grant.
- **Most specific path wins** when several rules match.

Roles are presets over the same mechanism, and per-user rules refine them:

```bash
contextd user role alice contributor
contextd acl deny alice spaces/team/finance/
contextd policy test alice read spaces/team/deploy.md
```

Every decision lands in a hash-chained audit log, so "who could read this, and who did" is answerable after the fact:

```bash
contextd audit list
contextd audit verify     # detects tampering with the log itself
```

## Freshness

Context rots quietly. An AI confidently repeating a deploy process you replaced six months ago is a bug you cannot see, and no amount of access control catches it.

So a file can declare its own expiry and owner:

```yaml
---
last-validated: 2026-07-26
stale-after: 30d
owner: platform-team
---
```

```bash
contextd freshness check          # what is stale, who owns it, since when
contextd freshness nag --server   # emit freshness.stale webhooks
contextd freshness validate team/deploy.md
```

Nothing expires automatically — a human confirms the content is still true and re-stamps it. The point is that going stale becomes visible instead of invisible.

## Three modes, one binary

The same `contextd` binary is all three; the mode lives in your config.

| Mode | Space lives | Sync | Typical user |
|---|---|---|---|
| **solo** | On your machine | None (optional git backend for backup) | One person |
| **client** | Mirrored from a server | `pull` / `push` | Team member |
| **server** | On the server | Serves clients | Whoever runs it |

## Three surfaces, one core

The **CLI is primary**: every capability lives there first. The terminal UI (`contextd tui`) and the server's web console are presentations of the same operations — same core, same permissions, same result. Use whichever suits the moment.

A capability that exists in a UI but not in the CLI is treated as a defect, not a feature.

## Where next

- [Quickstart](quickstart.md) — put this into practice
- [CLI reference](cli.md) — the full command surface
- [Auth & ACL](auth-acl.md) — policies in detail
- [Templates](templates.md) — starting shapes for a space
