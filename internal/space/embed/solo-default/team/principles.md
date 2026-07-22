---
freshness: current
last-validated: 2026-07-22
stale-after: 90d
confidence: medium
importance: high
---

# Principles

How we work. Weights tell the AI how hard to hold the rule.

```yaml
rules:
  - weight: critical
    text: "Never commit secrets or credentials"
  - weight: critical
    text: "Plan before building — doc/plan first, then implement"
  - weight: important
    text: "Keep documentation current with every change"
  - weight: important
    text: "Prefer the curated space over ad-hoc chat memory"
  - weight: preference
    text: "Prefer small, reviewable changes over large dumps"
```

| Weight | Meaning | AI behavior |
|---|---|---|
| `critical` | Non-negotiable | Always follow, flag violations |
| `important` | Strong default | Follow unless good reason not to |
| `preference` | Nice to have | Follow when easy, skip when costly |

Edit this file for your team (or for yourself in solo mode). Keep it short.
