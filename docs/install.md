# Install

`contextd` is a single static binary — no runtime, no daemon required, no external services for the default setup.

After installing, run `contextd init` for guided setup. The install scripts offer to do that for you.

!!! warning "Installed before the move to ORKCOM?"

    The project moved to the [`orkcom-tech`](https://github.com/orkcom-tech) organisation. A tap or bucket added under the old owner still resolves, but **no longer receives updates** — so `brew upgrade` will quietly keep you on an old version rather than tell you anything is wrong. Repoint it once:

    ```bash
    # Homebrew
    brew untap abyssmemes/tap
    brew tap orkcom-tech/tap
    brew install orkcom-tech/tap/contextd

    # Scoop
    scoop bucket rm contextverse
    scoop bucket add contextverse https://github.com/orkcom-tech/scoop-bucket
    scoop update contextd
    ```

    The winget package is now `OrkcomTech.Contextd`; an install from the old identifier will not upgrade across the rename, so reinstall from the new one. Installs from `install.sh`, `install.ps1` or a release archive need nothing — they fetch from the repository, and GitHub redirects the old path.

    Your context space is untouched by any of this. `~/.context` is yours and knows nothing about where the binary came from.

## macOS

=== "Homebrew (recommended)"

    ```bash
    brew tap orkcom-tech/tap
    brew install orkcom-tech/tap/contextd
    ```

    Our own tap — not `homebrew-core`. Source: [`orkcom-tech/homebrew-tap`](https://github.com/orkcom-tech/homebrew-tap).

=== "Install script"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/orkcom-tech/contextverse/main/scripts/install.sh | bash
    ```

## Linux

=== "Install script"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/orkcom-tech/contextverse/main/scripts/install.sh | bash
    ```

=== "deb / rpm"

    Every release publishes `.deb` and `.rpm` packages (GoReleaser nFPM). Grab the one for your architecture from [Releases](https://github.com/orkcom-tech/contextverse/releases).

    ```bash
    sudo dpkg -i contextd_*_amd64.deb     # Debian / Ubuntu
    sudo rpm -i contextd_*_x86_64.rpm     # Fedora / RHEL
    ```

Pin a specific version:

```bash
CONTEXTD_VERSION=v0.1.0 bash -c \
  "$(curl -fsSL https://raw.githubusercontent.com/orkcom-tech/contextverse/main/scripts/install.sh)"
```

## Windows

=== "Scoop (recommended)"

    ```powershell
    scoop bucket add contextverse https://github.com/orkcom-tech/scoop-bucket
    scoop install contextd
    ```

=== "Install script"

    ```powershell
    irm https://raw.githubusercontent.com/orkcom-tech/contextverse/main/scripts/install.ps1 | iex
    ```

Winget manifests exist as templates under [`packaging/winget/`](https://github.com/orkcom-tech/contextverse/tree/main/packaging/winget); submission to `winget-pkgs` is still a manual PR.

## Go

```bash
go install github.com/orkcom-tech/contextverse/cmd/contextd@latest
```

## From source

```bash
git clone https://github.com/orkcom-tech/contextverse.git
cd contextverse
make build          # → bin/contextd
make install
go test ./...
```

Needs a recent Go toolchain. No cgo.

## Verify

```bash
contextd version
```

Then set up shell completion, so the command groups are discoverable as you type:

=== "zsh"

    ```bash
    contextd completion zsh > "${fpath[1]}/_contextd" && compinit
    ```

=== "bash"

    ```bash
    contextd completion bash | sudo tee /etc/bash_completion.d/contextd
    ```

=== "fish"

    ```bash
    contextd completion fish > ~/.config/fish/completions/contextd.fish
    ```

=== "PowerShell"

    ```powershell
    contextd completion powershell | Out-String | Invoke-Expression
    ```

## Next

```bash
contextd init
```

See the [Quickstart](quickstart.md).

## Running as a service

Only needed if you are hosting a **server** for a team.

=== "Linux (systemd)"

    ```bash
    contextd server unit --server-dir /srv/contextverse | sudo tee /etc/systemd/system/contextd.service
    sudo systemctl enable --now contextd
    ```

=== "macOS (launchd)"

    Use [`deploy/contextd.plist`](https://github.com/orkcom-tech/contextverse/blob/main/deploy/contextd.plist).

=== "Windows (SCM)"

    From an Administrator shell, after `contextd init server`:

    ```powershell
    contextd server service install --server-dir $env:USERPROFILE\.contextverse-server
    contextd server service start
    # later: contextd server service stop | uninstall
    ```

    Details: [`deploy/contextd.winservice.md`](https://github.com/orkcom-tech/contextverse/blob/main/deploy/contextd.winservice.md).

Containers and Kubernetes: see [Deploy](deploy.md).

## Related repositories

| Repo | Role |
|---|---|
| [`contextverse-templates`](https://github.com/orkcom-tech/contextverse-templates) | Context-space and client-integration templates (Apache-2.0) |
| [`homebrew-tap`](https://github.com/orkcom-tech/homebrew-tap) | Homebrew formula |
| [`scoop-bucket`](https://github.com/orkcom-tech/scoop-bucket) | Scoop manifest |
