# Changelog

All notable changes to `contextd` are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) — with one caveat while the major version is `0`:

> **Pre-1.0 compatibility.** Minor releases may change commands, flags, the HTTP API and the on-disk layout. Pin an exact version if you depend on any of them. Breaking changes are called out under **Changed** with the migration in the same bullet.

Releases are cut automatically from `main` by CI; the tag and the GitHub release are created in the same run.

## [Unreleased]

### Added

- **The sync daemon can now start at login.** `contextd daemon install` writes a per-user launchd agent (macOS) or `systemd --user` unit (Linux); `daemon uninstall` removes it, `daemon unit` prints it without installing. Deliberately per-user, never system-wide: the daemon reads your token and syncs your space, so running it as root would use the wrong identity against a credential it should not be able to read.
- `contextd daemon logs [-n]` — the log has always been written to `.sync/daemon.log`, with no command to read it.
- `contextd daemon status` gained `--json` / `--yaml`, the last sync time, and whether autostart is installed.
- The client wizard now offers background sync. It existed with no way to discover it: nothing in setup mentioned it, and nothing installed it, so a working client went stale the moment its terminal closed.

### Changed

- **The daemon backs off when the server is unreachable.** A fixed ticker meant an identical failed request every interval forever — log noise, load on a server that is probably already unwell, and a flat battery on a laptop off the VPN. Failures now double the interval up to 15 minutes and the first success restores it. A single failure still retries at the normal interval, so one dropped packet does not push the next attempt out to minutes.

## [0.7.0] — 2026-07-27

The release that made the command surface match what the product claims. Most of the work is presentation rather than new capability, because the capability had outgrown the way it was exposed: 28 flat top-level commands, no guided path after install, no machine-readable output, and documentation describing several commands that were never built.

### Added

- **Guided setup.** `contextd init` with no subcommand is now a wizard: it picks the mode with you, explains every option as you choose it, offers a template from the catalog, sets up a storage backend, wires the AI tools it detects, and finishes by naming every file it created and what each is for.
- **`contextd init --reconfigure`** — change identity, AI tools, storage backend, default editor or (in client mode) the server connection, on an existing installation.
- **File editing from the CLI and TUI.** `contextd file edit <path>` checks the file out into `$EDITOR` and writes it back as a new version; `contextd file put <path> --from FILE|-` does the same from a file or stdin. In the TUI, <kbd>Enter</kbd> on a file opens an editor picker.
- **Server-side space management in the CLI** — `contextd space create|list|show|delete`. These existed only in the web console and the HTTP API, which inverted the project's rule that a capability lives in the CLI first.
- **Structured output.** Global `--json` / `--yaml` on `status`, `file list`, `file history`, `user list`, `space list`, `space show`, `pull` and `push`.
- **Meaningful exit codes** — `0` success, `1` usage or local state, `2` network/auth/permission, `3` compare-and-swap rejected. A pipeline can finally tell "retry later" from "you called this wrong".
- **opencode support** — detected via `opencode` on `PATH` or its config directories; context is delivered into a marked block in `AGENTS.md`.
- **Installers hand off to setup.** `install.sh` and `install.ps1` offer to run `contextd init` when a terminal is attached, and print the command when one is not.

### Changed

- **Root help is grouped by task** instead of listed alphabetically: set up and inspect · work on your context space · sync and storage · deliver context to AI tools · administer a server · interfaces and help.
- **TUI keys.** <kbd>Enter</kbd> on a file now edits it; version history moved to <kbd>V</kbd>. <kbd>v</kbd> previews the live file body — previously it did nothing at all on the file list.
- Help text in both the TUI and the web console is grouped with the same headings as the CLI.
- Empty states teach: the TUI on a directory with no space, and the server TUI on an uninitialized data directory, now name the path they checked and the command to run.
- Internal phase numbers removed from user-facing command descriptions.

### Fixed

- **Client integrations no longer overwrite files they do not own.** `applySlot` ignored the `merge:` field declared in every integration and always rewrote the whole target. For `AGENTS.md` — hand-written, and read by several agents — the first `contextd activate` would have destroyed it. `marked-block` merging is now implemented: only the delimited region belongs to contextd, re-runs replace rather than stack, and a half-written block is refused instead of guessed at.
- GUI editors are always launched with their wait flag, including when `$EDITOR` omits it. Without it the editor forks, the caller reads the file back unedited, and the user's work is silently discarded.
- Two tests that had been failing CI: the integration test posted to the console login without the CSRF handshake a browser performs, and an audit test asserted Unix permission bits on Windows, which does not implement them.

### Security

- **Editing is refused wherever the shell escape is refused.** A text editor is a shell escape (`:!sh`, `^R^X`, `M-!`), so allowing it in the SSH-served admin TUI would hand a shell to the locked-down service account and bypass RBAC — the exact hole the existing shell-escape gate closes.
- `contextd space delete` requires `--yes` and reports the file and byte count it is about to destroy, because it removes the version history with it.
- Carries forward the hardening from the same cycle: per-resource authorization in the console and event stream, every filesystem sink behind one path validator, bounded token lifetime, and no trust in forwarded identity headers.

## [0.6.0] — 2026-07-23

### Changed

- Space UI restyled to match the cloud SPA shell.

## [0.5.0] — 2026-07-23

### Added

- HTTP API for users and policies.
- A bootstrap admin token is written on server init.

### Fixed

- CI module downloads retry through proxy flakes.

## [0.4.0] — 2026-07-23

### Added

- DNS-01 ACME, OTLP tracing, Windows service support, and stateless HA.
- Web UI brought to parity with the CLI for the operations it exposes.

### Fixed

- Windows service test no longer attempts a real install on CI.

## [0.3.0] — 2026-07-23

### Changed

- CI and minor releases are gated on product code paths, so documentation and workflow edits no longer cut a release. A manual run skips the gate deliberately.

## [0.2.0] — 2026-07-23

### Added

- Docker and Helm templates for running the server.

## [0.1.0] — 2026-07-23

The first substantial release: server, storage, governance and the AI-delivery surfaces.

### Added

- **Server** with pluggable storage (local filesystem, git, S3, SQL), an admin web UI, and path-based ACL.
- **Per-file versioning** (`FileLog`), the terminal UI, and session-start plugins.
- **Governance** — audit log, outbound webhooks, per-user ACL with deny-wins path exceptions, rate limits, storage quotas, and freshness nagging.
- **Secret-scan guardrail** on push and on file writes.
- **Observability** — Prometheus metrics and an SSE live event stream.
- **ACME TLS** and an OSS auth-method registry.
- **MCP server** on stdio (`context_status` / `list` / `get` / `search`).
- Template catalog fetching and caching, community client-integrations, shell completions, a ChatGPT export bundle, Linux packages, MkDocs user documentation, and branch-aware CI with automatic minor releases.

## [0.0.1] — 2026-07-22

### Added

- Initial scaffold: the `contextd` binary and its installers in one repository, under BUSL-1.1.
- Core solo workflow — `init solo`, the space model, `activate`, `status`.
- Install scripts and the GoReleaser pipeline.

[Unreleased]: https://github.com/abyssmemes/contextverse/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/abyssmemes/contextverse/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/abyssmemes/contextverse/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/abyssmemes/contextverse/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/abyssmemes/contextverse/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/abyssmemes/contextverse/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/abyssmemes/contextverse/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/abyssmemes/contextverse/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/abyssmemes/contextverse/releases/tag/v0.0.1
