# Templates

A template decides what your context space looks like on day one, and how a new AI client gets wired. Both kinds live in one place: [`contextverse-templates`](https://github.com/orkcom-tech/contextverse-templates) (Apache-2.0).

## Context-space templates

```bash
contextd template list                          # browse the catalog
contextd init solo --template team-engineering  # start from one
contextd space seed --template <name> --force   # re-seed an existing space
```

`contextd init` offers the catalog during setup, with a description for each entry, so you rarely need `template list` by hand.

| Template | Best for |
|---|---|
| `solo-default` | Individual use. The canonical space model — start here if unsure. |
| `team-engineering` | An engineering squad: shared standards, multiple projects, ADRs |
| `devops-platform` | DevOps / SRE / platform: systems map and change discipline |
| `product-startup` | Founders and early product: bets, users, pivots |
| `client-engagement` | Consultants and agencies: per-client isolation |

Every template carries a `TEMPLATE.md` explaining who it is for and why it is shaped the way it is. Read that rather than the file list — the reasoning is the useful part.

!!! note "Re-seeding keeps your identity"
    `contextd space seed` rewrites the template files but leaves `identity/me.md` alone. That file is seeded once and then belongs to you.

## Client-integration templates

These teach `contextd` how to detect an AI client and where to put its session-start context. **Adding support for a new AI tool is a pull request to the templates repository — not a release of the binary.**

```bash
contextd plugin list       # what is known, and what is detected here
contextd plugin install    # wire the detected ones
contextd plugin refresh    # fetch the latest community integrations
```

The well-known clients ship embedded in the binary, so `contextd` works offline; the catalog adds to them, and embedded IDs win on conflict.

### What contextd writes, per client

| Client | Slot | Nature |
|---|---|---|
| Claude Code | `SessionStart` hook in `settings.json` | **Live** — re-reads the space every session |
| Cursor | `.cursor/rules/contextverse.mdc` | Snapshot, refreshed on `activate` |
| Windsurf | `.windsurfrules` | Snapshot |
| GitHub Copilot | `.github/copilot-instructions.md` | Snapshot |
| opencode | `AGENTS.md`, marked block | Snapshot; your own content preserved |
| MCP clients | `contextd mcp serve` over stdio | Live tools |
| ChatGPT, other web UIs | — none — | `contextd export --format chatgpt`, manual upload |

### Files contextd shares with you

Some slots are dedicated files that belong entirely to `contextd`, and it rewrites them wholesale. Others — `AGENTS.md` above all — you wrote yourself, and other tools read them too. For those, `contextd` owns only a delimited block:

```markdown
Your own instructions, untouched.

<!-- >>> contextverse >>> -->
…generated pointer to your space…
<!-- <<< contextverse <<< -->

More of your own text, also untouched.
```

Re-running `activate` replaces that block rather than appending another copy. If the opening marker is present without its closing one, `contextd` refuses and asks you to fix it — guessing where your text resumed is how content gets eaten.

## Writing your own

Both kinds are directories of files with one YAML manifest, and both require a human-readable rationale file — `TEMPLATE.md` or `INTEGRATION.md`. A pull request without one is rejected, because six months later nobody can tell why the template is shaped the way it is.

The full schema, the mechanism and merge-strategy tables, and the honesty rule for stating a client's real ceiling are in the [templates repository README](https://github.com/orkcom-tech/contextverse-templates#readme).
