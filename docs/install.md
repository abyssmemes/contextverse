# Install

## macOS (Homebrew — recommended)

```bash
brew tap abyssmemes/tap
brew install abyssmemes/tap/contextd
```

Own tap: [`abyssmemes/homebrew-tap`](https://github.com/abyssmemes/homebrew-tap) (not `homebrew-core`).

## macOS / Linux (script)

```bash
curl -fsSL https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.sh | bash
```

Pin a version:

```bash
CONTEXTD_VERSION=v0.1.0 bash -c \
  "$(curl -fsSL https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.sh)"
```

## Windows (Scoop — recommended)

```powershell
scoop bucket add contextverse https://github.com/abyssmemes/scoop-bucket
scoop install contextd
```

### Windows service

After `contextd init server` (or UI setup), register with SCM (Administrator shell):

```powershell
contextd server service install --server-dir $env:USERPROFILE\.contextverse-server
contextd server service start
# stop / uninstall:
contextd server service stop
contextd server service uninstall
```

Details: [`deploy/contextd.winservice.md`](https://github.com/abyssmemes/contextverse/blob/main/deploy/contextd.winservice.md).

## Windows (script)

```powershell
irm https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.ps1 | iex
```

## Linux packages

GitHub Releases include `.deb` / `.rpm` (GoReleaser nFPM). Download from [Releases](https://github.com/abyssmemes/contextverse/releases).

## From source / Go

```bash
go install github.com/abyssmemes/contextverse/cmd/contextd@latest
# or
git clone https://github.com/abyssmemes/contextverse.git
cd contextverse && make build && make install
```

## Verify

```bash
contextd version
contextd completion zsh   # also: bash | fish | powershell
```

## Related repos

| Repo | Role |
|---|---|
| [`contextverse-templates`](https://github.com/abyssmemes/contextverse-templates) | Community space templates + client-integrations |
| [`homebrew-tap`](https://github.com/abyssmemes/homebrew-tap) | Homebrew formula |
| [`scoop-bucket`](https://github.com/abyssmemes/scoop-bucket) | Scoop manifest |

Winget manifests are templates under [`packaging/winget/`](https://github.com/abyssmemes/contextverse/tree/main/packaging/winget) (manual PR to `winget-pkgs` for now).
