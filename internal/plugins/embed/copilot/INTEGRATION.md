# GitHub Copilot — client integration

## Client

[GitHub Copilot](https://github.com/features/copilot). Instructions slot: `.github/copilot-instructions.md`.

## Mechanism

`instructions-slot` — repository instruction file loaded by Copilot in supported surfaces.

## Honest ceiling

**Static snapshot** refreshed on activate/plugin install. Not a live hook. Delivery ≠ obedience.

## Detection

Any of: `gh` on `PATH`, `~/.config/github-copilot/`, or `~/.copilot/`.
