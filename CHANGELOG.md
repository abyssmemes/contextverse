# Changelog

All notable changes to `contextd` are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) — with one caveat while the major version is `0`:

> **Pre-1.0 compatibility.** Minor releases may change commands, flags, the HTTP API and the on-disk layout. Pin an exact version if you depend on any of them. Breaking changes are called out under **Changed** with the migration in the same bullet.

Releases are cut automatically from `main` by CI; the tag and the GitHub release are created in the same run.

## [Unreleased]

### Added

- **A test fixture for the state real installations are actually in.** `internal/testspace` builds a space as a pre-v0.7 build left it: documents on disk, an empty version log, no `.contextverse`, `space-index.md` frozen in the old format. Every test in this repository used to start from a space the current build had just created — the one state a user is almost never in. Four shipped bugs lived exclusively in the other state, which is why a green suite meant nothing; they were found by a person on a second laptop instead.
- **Golden snapshots of what the TUI draws.** Each tab is rendered at a fixed size against that fixture and compared as text (`go test ./internal/tui -update` to rewrite). The three tabs that contradicted each other were each individually defensible — every function returned what its own test expected. Nobody looked at the screen, where "8 files" and "(no tracked files)" sat two keystrokes apart. That now appears in a diff, in the words the user reads.
- **Tests that run whole commands rather than the functions inside them.** `contextd init solo` end to end: flags alone with no extra `--non-interactive`, refusing to overwrite without `--force`, overwriting with it, and a fresh space having tracked files immediately. Each covers a bug that shipped because the function worked and the command did not.
- **Link validation for the local console.** Every `href` a local page renders must resolve to a route the local mux actually registers. Checking status codes was never enough — a page can answer `200` and still be full of links that do not.

- **The sync daemon can now start at login.** `contextd daemon install` writes a per-user launchd agent (macOS) or `systemd --user` unit (Linux); `daemon uninstall` removes it, `daemon unit` prints it without installing. Deliberately per-user, never system-wide: the daemon reads your token and syncs your space, so running it as root would use the wrong identity against a credential it should not be able to read.
- `contextd daemon logs [-n]` — the log has always been written to `.sync/daemon.log`, with no command to read it.
- `contextd daemon status` gained `--json` / `--yaml`, the last sync time, and whether autostart is installed.
- The client wizard now offers background sync. It existed with no way to discover it: nothing in setup mentioned it, and nothing installed it, so a working client went stale the moment its terminal closed.
- **`contextd ui`** — a local web console for solo and client spaces, on demand: it prints a URL, serves until Ctrl-C, and exits. Bound to loopback, a fresh one-time key per run, `Host` and `Origin` validated so a page in another tab cannot drive it, and double-submit CSRF on writes. `contextd ui install` keeps it running for people who want that, after confirming the trade. Deliberately not on by default: a standing web server with write access to your context files, for someone who may never open it, is a door left open — the TUI covers the same ground and listens on nothing. It shares the server console's templates and stylesheet rather than duplicating them.
- **`contextd search <query>`** — find text in your own space: paths and contents, case-insensitive substring by default, `--regex`, `--path` glob, `-l` for filenames only, and structured output. The MCP server has exposed search to AI clients since v0.1.0, so an assistant could look through your space and you could not. Both now run the same `internal/search`, because two implementations of "what is in here" is how a tool starts telling a person and their assistant different things.
- **`contextd file diff <path>`** — a unified diff between two versions, defaulting to the previous one against the current: the usual question of what a change actually did. `--from` / `--to` for any pair, `--stat` for counts, `-U` for context width. `file history` said a version existed and `file get` printed one; nothing showed what moved.
- **`space-index.md` and `team/space-map.md` are now generated from the graph.** The index came from a hardcoded format string whose Dependencies column was a literal em dash, and the map was a drawing seeded once at init that never learned about a file again — while `context-entry.md` told every AI to read the index for "what exists". Both are rewritten through the FileLog, so regenerating them is an ordinary versioned edit, and identical output creates no new version.
- **`contextd context inject --mode map`** — hand an AI the map and the means to fetch, instead of the documents. On a ten-document space the map is 222 approximate tokens against 532 for the entry set, and the gap widens with the space because the entry set grows with content while the map grows with its budget. **Opt-in on purpose:** which one a model actually does better with has not been measured, and switching the default on an unmeasured belief is the mistake the benchmark exists to prevent.
- **The graph has surfaces.** A Graph tab in the TUI (tab 6) where <kbd>Enter</kbd> opens a document's connections and again walks to one of them, and a Graph page in the local console listing every document with what it links to, the broken links, and the orphans. The console offers Mermaid source rather than drawing the graph: rendering one needs a layout engine, and vendoring a megabyte of JavaScript for a single page is not a trade worth making.
- **MCP gained `context_map` and `context_neighbors`**, so an assistant can navigate the space — ask what a document connects to — rather than be handed all of it.
- **`contextd graph`** — the map of your space, derived from the links your documents already contain. `[[wikilinks]]`, Markdown links and bare paths all resolve; backlinks, orphans and broken links fall out of it; ranking is PageRank over the links, lifted by a document's declared `importance` and cut in half when it has gone stale. `graph <path>` shows a document's neighbourhood, `--orphans` and `--broken` the two ways a map comes apart, `--format mermaid` a picture. `contextd status` gained a one-line summary.
- **Documentation that points at dead code is now visible.** A path like `./scripts/deploy.sh` inside a project's document is resolved against the checkout `activate` recorded and reported when it no longer exists. Nothing else connects written procedure to the code it describes: editors map your code and know nothing of your decisions, note tools map your notes and know nothing of your repository.
- **`contextd activate` now records where a project lives.** It has always known the working directory and the project name and persisted neither, so the space could describe a project without knowing where on disk it was. That anchor is what will let the context graph check a document mentioning `./scripts/deploy.sh` against real files. Per-machine by design: a checkout path is local truth, not something to push to teammates.

### Changed

- **The console is minimal brutalism now.** It led with a pink-to-violet gradient sidebar, a serif display face, soft shadows and 16px radii — decoration carrying no information, on a tool for people who live in a terminal. One monospace family (every value on screen is a path, a version or a command, read character by character), flat surfaces with hairline borders, square corners, and colour reserved for meaning: accent for the current thing, red for danger, amber for stale. Long content no longer pushes the page sideways. Web fonts dropped, so a machine that is offline or behind a proxy renders what everyone else sees — and a tool that edits your files does not announce every page load to a font CDN.
- A **Documentation** link now sits in both sidebars.

### Fixed

- **Nothing in the local console could be opened by clicking it.** The file table built its links from the space's display name, which on a server is also its route and in the local console is the directory's basename — so every row pointed at `/ui/spaces/.context/...` while the console answers on `/ui/spaces/local`. Routing identity and display name are separate fields now.
- **The console's menu offered a Freshness page it does not serve.** One dead link was removed when the console shipped and the one beside it was missed. There is now a test that resolves every rendered link, which is how both were found.
- **The console listed the version log rather than the space**, so a space carried across an upgrade looked empty in the browser while its documents sat on disk. The same fix had already been made in the CLI and the TUI; the third surface was missed. All three now share one implementation (`internal/spacefiles`) — four hand-written copies of "what is in this space" agreed only on spaces this build had just created, and three fixes for one bug is the usual arithmetic of duplicated logic.

- **The Files tab and `file list` showed nothing while the space was full of files.** They listed the version log; the Space tab counted the directory. So one tab reported "identity 1 files, team 3 files" and the next said "(no tracked files)" with nothing to select and nothing to open — the tool contradicting itself about its own contents, which is what made it feel broken. Both now list what is in the space, marking a file with no version yet as `—`; opening one records `v1`. `space adopt` remains for backfilling history deliberately, but nobody has to run it to see their own documents.
- **The setup wizard asked whether to overwrite an existing space, was told yes, and then refused.** It confirmed the decision but never passed it to the code that creates the space, so every remaining question was answered before the run failed — the worst possible place to fail. `--force` was dropped on the same path, making the flag a no-op exactly when it was needed.
- **Spaces created before version tracking existed showed as empty.** Their documents were on disk with nothing in the version log, so `contextd file list` said "(no files)" while eight of them sat right there, the TUI Files tab was blank, and there was nothing to open — the tool contradicting itself about its own contents. `contextd space adopt` records them as `v1`, and `activate` now does it automatically when it finds a log that knows nothing about a tree that has content. The earlier fix only covered newly created spaces; every space that already existed stayed broken.
- **`contextd init solo` gave four bare prompts instead of the guided setup.** The wizard was added to `contextd init`, and `init solo` — the command everyone types out of habit, and the one every version of the documentation has printed — was left on the old path. Run interactively with no flags, it now runs the same guided setup. `init client` likewise.
- **Flags alone were not enough to set up in one command.** Passing `--name` and `--role` still stopped to ask for them unless `--non-interactive` was also given: a flag naming the mechanism rather than the intent. Supplying flags now means using them. `--non-interactive` remains for scripts that want the guarantee.
- **The local console linked to a page that does not exist there.** The shared space template offered "All spaces", which belongs to a server.

- **A relative `--dir` sent config writes to the wrong file.** `space_root` was stored as whatever string `--dir` carried at init, and `Save` resolved it against the *calling* working directory — so running `contextd activate` inside a project wrote a stray `config.yaml` under that project and left the real one untouched. In client mode that silently dropped `Sync.LastHead`, so the client re-pulled the whole space every run and pushed against a stale head, inviting conflicts that were not real. The space root is now resolved to an absolute path on load and on save.
### Fixed

- **A fresh space had no version history at all.** `space.Create` writes the template straight to the working tree, so nothing it seeded was ever recorded in the version log: `contextd file list` reported "(no files)" on a space containing eleven Markdown files, `file history` was empty for every one of them, and the Files tab in the TUI showed nothing. History began at the first write through contextd rather than when the content began. Setup now records the seeded tree as `v1`.

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
