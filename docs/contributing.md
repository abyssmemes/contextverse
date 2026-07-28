# Contributing

Early-stage project. Issues and design discussion are welcome.

## Docs (this site)

User documentation is the **source of truth** in this repository under [`docs/`](https://github.com/orkcom-tech/contextverse/tree/main/docs).

```bash
python3 -m venv .venv-docs
source .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs serve    # http://127.0.0.1:8000
mkdocs build --strict
```

Edit Markdown, open a PR. The `docs` GitHub Actions workflow publishes to GitHub Pages on pushes to `main` that touch docs/config.

## Code

```bash
make test
make test-integration   # Linux + Docker (MinIO/Postgres)
make build
```

### Branches

- Feature work: `dev/<name>` (no CI)
- Validation: `test/<name>` (tests only)
- Ship: PR → `main` (auto minor release when green)
- Hotfix: `release/X.Y.Z` + manual workflows — see [Packaging & releases](packaging.md)

### Changelog

Anything a user would notice goes in [`CHANGELOG.md`](https://github.com/orkcom-tech/contextverse/blob/main/CHANGELOG.md) under `## [Unreleased]`, in the same pull request as the change.

Because `main` releases itself the moment CI is green, there is no later moment to write it down — an entry that isn't in the PR simply never gets written. Use the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) sections (`Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`), and describe the effect on the user rather than the diff:

> **Fixed** — GUI editors are now launched with their wait flag. Without it the editor forked and your edit was silently discarded.

not

> **Fixed** — added `--wait` in `editor.go`.

Pure refactors, test-only changes and internal cleanups don't need an entry. When a change breaks compatibility, put the migration in the same bullet.

## License / DCO

Code is [BUSL-1.1](https://github.com/orkcom-tech/contextverse/blob/main/LICENSE). A CLA/DCO for external PRs will land when the project formally accepts outside contributions.
