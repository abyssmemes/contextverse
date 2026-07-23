# Contributing

Early-stage project. Issues and design discussion are welcome.

## Docs (this site)

User documentation is the **source of truth** in this repository under [`docs/`](https://github.com/abyssmemes/contextverse/tree/main/docs).

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

## License / DCO

Code is [BUSL-1.1](https://github.com/abyssmemes/contextverse/blob/main/LICENSE). A CLA/DCO for external PRs will land when the project formally accepts outside contributions.
