# Packaging helpers for contextd

**Shipped via GitHub Releases (GoReleaser):**

| Artifact | How |
|---|---|
| `.tar.gz` / `.zip` | archives (install scripts + brew) |
| `.deb` / `.rpm` | `nfpms` in `.goreleaser.yaml` |

**Package manager taps / buckets (self-hosted):**

| Manager | Repo | Install |
|---|---|---|
| Homebrew | [`orkcom-tech/homebrew-tap`](https://github.com/orkcom-tech/homebrew-tap) | `brew tap orkcom-tech/tap && brew install orkcom-tech/tap/contextd` |
| Scoop | [`orkcom-tech/scoop-bucket`](https://github.com/orkcom-tech/scoop-bucket) | `scoop bucket add contextverse https://github.com/orkcom-tech/scoop-bucket` then `scoop install contextd` |
| Winget | [`packaging/winget/`](./winget/) templates | PR to `microsoft/winget-pkgs` on release cut (manual) |

## CI publish path

Authority: Immaterium [[contextverse-ci-release-branches]] — `/Projects/contextverse/docs/planning/contextverse-ci-release-branches.md`.

On green `main` (auto **minor**), manual **Bump major**, or manual **Release branch → deploy** (patch):

1. Tag `vX.Y.Z` + GoReleaser GitHub Release
2. `scripts/ci/publish-packages.sh vX.Y.Z` bumps brew + scoop (needs repo secret `PACKAGING_TOKEN`)
3. Push pin branch `release/X.Y.Z`

Manual local bump (same scripts the CI runs):

```bash
# in homebrew-tap / scoop-bucket clones:
./scripts/bump-formula.sh v0.2.0
./scripts/bump-manifest.sh v0.2.0
```

Or from the contextverse checkout:

```bash
PACKAGING_TOKEN=ghp_… ./scripts/ci/publish-packages.sh v0.2.0
```
