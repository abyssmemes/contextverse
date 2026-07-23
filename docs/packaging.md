# Packaging & releases

How `contextd` is built, tagged, and published to package managers.

## Artifacts (GitHub Releases)

GoReleaser produces:

- Archives (`.tar.gz` / `.zip`) for macOS / Linux / Windows (amd64 + arm64)
- `.deb` / `.rpm` (nFPM)
- `checksums.txt`

## Package managers

| Manager | Repo | Update path |
|---|---|---|
| Homebrew | [`abyssmemes/homebrew-tap`](https://github.com/abyssmemes/homebrew-tap) | CI runs `bump-formula.sh` |
| Scoop | [`abyssmemes/scoop-bucket`](https://github.com/abyssmemes/scoop-bucket) | CI runs `bump-manifest.sh` |
| Winget | templates in `packaging/winget/` | manual PR for now |

## Branch / CI policy

| Branch | Automatic CI | Publish |
|---|---|---|
| `dev/**` | none | — |
| `test/**` | tests **if code paths changed** | no |
| `main` | tests → **minor** release **if code paths changed** | yes |
| `release/X.Y.Z` | none on push | manual **test** / **deploy** (patch) |
| PRs | tests **if code paths changed** | no |
| docs / deploy templates / README only | `docs.yml` or nothing | **no version bump** |
| **Manual** Actions → **ci** → Run workflow | skip change gate → tests → **minor release** | yes |

Code paths that start `ci.yml` on push/PR: `**/*.go`, `go.mod`/`go.sum`, `Makefile`, `cmd/**`, `internal/**`, `scripts/**`, `.goreleaser.yaml`, release workflow YAML.

**Manual run** always does the full pipeline (tests + new minor + brew/scoop), even with no code diff.

Version bumps:

- **main** (code change) → next **minor**
- **Manual ci** → next **minor**
- **Bump major** workflow → next **major**
- **release-branch → deploy** → next **patch** + new `release/X.Y.Z` pin

Scripts: `scripts/ci/next-version.sh`, `publish-packages.sh`, `create-release-refs.sh`.

### Secret

Repo secret `PACKAGING_TOKEN` — fine-grained PAT with **Contents: Read and write** on `homebrew-tap`, `scoop-bucket`, and `contextverse` (for checksum download).

## Local package bump

```bash
PACKAGING_TOKEN=github_pat_… ./scripts/ci/publish-packages.sh v0.2.0
```
