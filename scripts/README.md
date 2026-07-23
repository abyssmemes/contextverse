# Install scripts

Cross-platform installers for the `contextd` binary.

CI release helpers live under [`ci/`](./ci/) (`next-version.sh`, `publish-packages.sh`, `create-release-refs.sh`) — see Immaterium `docs/planning/contextverse-ci-release-branches.md`.

| File | Platform |
|---|---|
| [`install.sh`](./install.sh) | macOS / Linux |
| [`install.ps1`](./install.ps1) | Windows (PowerShell) |

## Quick install

**macOS (Homebrew — recommended)**

```bash
brew tap abyssmemes/tap
brew install abyssmemes/tap/contextd
```

Own tap: [`homebrew-tap`](https://github.com/abyssmemes/homebrew-tap) (not `homebrew-core`).

**macOS / Linux (script)**

```bash
curl -fsSL https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.ps1 | iex
```

Pin a version:

```bash
CONTEXTD_VERSION=v0.0.1 bash scripts/install.sh
```

## What the installer does

1. Detects OS/arch.
2. Downloads the matching **GitHub Release** asset (`contextd_<ver>_<os>_<arch>.tar.gz` / `.zip`) and verifies `checksums.txt` when present.
3. Installs into `/usr/local/bin` (if writable) or `~/.local/bin` (Windows: `%LOCALAPPDATA%\contextverse\bin`, and adds it to the user `PATH`).
4. If no release is available (or download fails) — **falls back to `go install`**.
5. Prints `contextd version` and next steps (`init solo` → `activate`).

## Private repo note

OSS repos are public. A token is only needed if you pin a private fork or use `go install` against a private mirror (`GITHUB_TOKEN` / `gh auth login`).

## Releases

Tagged versions (`v*`) are built by GoReleaser (`.github/workflows/release.yml`). Create a release:

```bash
git tag v0.0.1
git push origin v0.0.1
```
