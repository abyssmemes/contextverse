# solo-default

Official starter for a **single person** running ContextVerse in solo mode.

## Who it's for

- Individual developers / DevOps / makers who use **more than one AI tool** (Claude, Cursor, Copilot, …)
- People who want a clean personal context without standing up a server
- First-time ContextVerse users who need the canonical space-model layout

## Problem it solves

Without a shared personal layout, every AI tool gets a different half-remembered story: one `CLAUDE.md`, one Cursor rule, stale Notion paste. Switching tools means re-teaching who you are. This template gives **one portable space** shaped exactly like the documented model, ready for `contextd activate`.

## Design rationale

- **Identity-heavy, team-light.** In solo mode “team” is *your* working standards — still useful so principles and maps have a home, but not multi-user RBAC theatre.
- **One example project** (`example-project`) so `projects/` is never an empty mystery; rename or delete it immediately.
- **Importance weights** in principles so the AI has a signal-vs-noise contract from day one.
- **Deliberately no** multi-client folders, runbook forests, or roadmap epics — those belong in other templates.

## Space map

| Path | Role |
|---|---|
| `context-entry.md` | Universal read order for any AI |
| `space-index.md` | Compact inventory |
| `decisions.md` | Personal / local decision log |
| `identity/me.md` | You (filled by `init solo`) |
| `team/principles.md` | Your non-negotiables + weights |
| `team/skill-map.md` | What you can do / tools you use |
| `team/space-map.md` | How to navigate this space |
| `projects/example-project/` | Scaffold: `project.md`, `map.md`, `services/` |

## How to use after init

1. Finish `identity/me.md` if the wizard left gaps.
2. Edit `team/principles.md` — replace example preferences with yours.
3. Rename `projects/example-project` → your real project (or delete and add your own).
4. `cd <repo> && contextd activate`
5. Optional: `contextd mcp serve` for live tool access

## What does not belong here

- Production secrets, tokens, private keys
- Entire git repos under `services/` (notes only)
- Team-wide HR / management context (use `team-engineering`)
- Client NDA material (use `client-engagement`)

## Alternatives

| If you need… | Use |
|---|---|
| Shared eng standards across a squad | `team-engineering` |
| Infra / SRE / platform focus | `devops-platform` |
| Early product / founder context | `product-startup` |
| Per-client consulting boundaries | `client-engagement` |
