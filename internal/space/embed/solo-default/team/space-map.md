---
freshness: current
last-validated: 2026-07-22
stale-after: 90d
confidence: medium
importance: medium
---

# Space Map

How to navigate this context space.

```
context-entry.md          ← start here (every AI)
space-index.md            ← compact inventory
decisions.md              ← why things are the way they are
identity/me.md            ← personal layer (local)
team/
  principles.md           ← rules + importance weights
  skill-map.md            ← what we can do
  space-map.md            ← this file
projects/
  <name>/
    project.md            ← what the project is + dependencies
    map.md                ← connections / deep-dive pointers
    services/             ← repo/service notes (not synced as code)
```

## Session flow

1. Read `context-entry.md` → entry set
2. Check `space-index.md` for the active project
3. Load `projects/<name>/project.md` (+ `map.md` if needed)
4. Deep-dive only the files the task requires
