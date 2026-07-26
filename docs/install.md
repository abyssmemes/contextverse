# Install

`contextd` is a single static binary — no runtime, no daemon required, no external services for the default setup.

After installing, run `contextd init` for guided setup. The install scripts offer to do that for you.

## macOS

=== "Homebrew (recommended)"

    ```bash
    brew tap abyssmemes/tap
    brew install abyssmemes/tap/contextd
    ```

    Our own tap — not `homebrew-core`. Source: [`abyssmemes/homebrew-tap`](https://github.com/abyssmemes/homebrew-tap).

=== "Install script"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.sh | bash
    ```

## Linux

=== "Install script"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.sh | bash
    ```

=== "deb / rpm"

    Every release publishes `.deb` and `.rpm` packages (GoReleaser nFPM). Grab the one for your architecture from [Releases](https://github.com/abyssmemes/contextverse/releases).

    ```bash
    sudo dpkg -i contextd_*_amd64.deb     # Debian / Ubuntu
    sudo rpm -i contextd_*_x86_64.rpm     # Fedora / RHEL
    ```

Pin a specific version:

```bash
CONTEXTD_VERSION=v0.1.0 bash -c \
  "$(curl -fsSL https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.sh)"
```

## Windows

=== "Scoop (recommended)"

    ```powershell
    scoop bucket add contextverse https://github.com/abyssmemes/scoop-bucket
    scoop install contextd
    ```

=== "Install script"

    ```powershell
    irm https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.ps1 | iex
    ```

Winget manifests exist as templates under [`packaging/winget/`](https://github.com/abyssmemes/contextverse/tree/main/packaging/winget); submission to `winget-pkgs` is still a manual PR.

## Go

```bash
go install github.com/abyssmemes/contextverse/cmd/contextd@latest
```

## From source

```bash
git clone https://github.com/abyssmemes/contextverse.git
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

    Use [`deploy/contextd.plist`](https://github.com/abyssmemes/contextverse/blob/main/deploy/contextd.plist).

=== "Windows (SCM)"

    From an Administrator shell, after `contextd init server`:

    ```powershell
    contextd server service install --server-dir $env:USERPROFILE\.contextverse-server
    contextd server service start
    # later: contextd server service stop | uninstall
    ```

    Details: [`deploy/contextd.winservice.md`](https://github.com/abyssmemes/contextverse/blob/main/deploy/contextd.winservice.md).

Containers and Kubernetes: see [Deploy](deploy.md).

## Related repositories

| Repo | Role |
|---|---|
| [`contextverse-templates`](https://github.com/abyssmemes/contextverse-templates) | Context-space and client-integration templates (Apache-2.0) |
| [`homebrew-tap`](https://github.com/abyssmemes/homebrew-tap) | Homebrew formula |
| [`scoop-bucket`](https://github.com/abyssmemes/scoop-bucket) | Scoop manifest |
