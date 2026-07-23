# Packaging helpers for contextd

**Shipped via GitHub Releases (GoReleaser):**

| Artifact | How |
|---|---|
| `.tar.gz` / `.zip` | archives (install scripts + brew) |
| `.deb` / `.rpm` | `nfpms` in `.goreleaser.yaml` |

**Manual / community buckets (templates here):**

| Manager | Path |
|---|---|
| Scoop | [`scoop/contextd.json`](./scoop/contextd.json) — add a bucket that vendors this manifest, or copy into your bucket |
| Winget | [`winget/`](./winget/) — example manifest tree; PR to `microsoft/winget-pkgs` when cutting a release |

Homebrew remains the primary macOS path: [`abyssmemes/homebrew-tap`](https://github.com/abyssmemes/homebrew-tap).

## Scoop (example)

```powershell
# After publishing a release, update Version + hashes in scoop/contextd.json, then:
scoop bucket add contextverse https://github.com/abyssmemes/scoop-bucket   # when bucket exists
scoop install contextd
```

Until `abyssmemes/scoop-bucket` exists, point Scoop at a local bucket:

```powershell
scoop bucket add local C:\path\to\contextverse\packaging\scoop-bucket
# place contextd.json in that bucket root
scoop install contextd
```

## Winget

Validate locally with [wingetcreate](https://github.com/microsoft/winget-create) against `winget/`, then open a PR to `microsoft/winget-pkgs`.
