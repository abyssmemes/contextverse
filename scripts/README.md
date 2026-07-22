# Install scripts

Cross-platform installers for the `contextd` binary.

| File | Platform |
|---|---|
| [`install.sh`](./install.sh) | macOS / Linux |
| [`install.ps1`](./install.ps1) | Windows (PowerShell) |

## Quick install

**macOS / Linux**

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

While this repository is **private**, release downloads and `go install` need auth:

```bash
gh auth login
# or
export GITHUB_TOKEN=ghp_...
```

The script picks up `GITHUB_TOKEN`, `GH_TOKEN`, or `gh auth token`.

## Releases

Tagged versions (`v*`) are built by GoReleaser (`.github/workflows/release.yml`). Create a release:

```bash
git tag v0.0.1
git push origin v0.0.1
```
