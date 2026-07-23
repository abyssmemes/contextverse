# Packaging helpers for contextd

**Shipped via GitHub Releases (GoReleaser):**

| Artifact | How |
|---|---|
| `.tar.gz` / `.zip` | archives (install scripts + brew) |
| `.deb` / `.rpm` | `nfpms` in `.goreleaser.yaml` |

**Package manager taps / buckets (self-hosted):**

| Manager | Repo | Install |
|---|---|---|
| Homebrew | [`abyssmemes/homebrew-tap`](https://github.com/abyssmemes/homebrew-tap) | `brew tap abyssmemes/tap && brew install abyssmemes/tap/contextd` |
| Scoop | [`abyssmemes/scoop-bucket`](https://github.com/abyssmemes/scoop-bucket) | `scoop bucket add contextverse https://github.com/abyssmemes/scoop-bucket` then `scoop install contextd` |
| Winget | [`packaging/winget/`](./winget/) templates | PR to `microsoft/winget-pkgs` on release cut |

After each `v*` release:

- Brew: `homebrew-tap/scripts/bump-formula.sh vX.Y.Z`
- Scoop: `scoop-bucket/scripts/bump-manifest.sh vX.Y.Z`
