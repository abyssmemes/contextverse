## What this changes

<!-- What behaviour is different afterwards. Not a list of files. -->

## Why

<!-- The problem. If it fixes an issue, link it. -->

## How it was verified

<!--
Not "tests pass" — which tests, and how you know they would have failed before.
A test that cannot fail is a test that proves nothing, and several bugs have
shipped here past a green suite for exactly that reason.
-->

- [ ] `go test ./...` passes
- [ ] `gofmt -l` is clean for the files I touched
- [ ] For a bug fix: the test fails without the fix (say how you checked)
- [ ] For behaviour a person sees: I ran the command and read the output, not just the exit code

## Documentation

- [ ] `--help` text is accurate for anything I added or changed
- [ ] `docs/` updated if a command, flag or output format changed
- [ ] `CHANGELOG.md` under `[Unreleased]`, written for someone deciding whether to upgrade

<!--
If the change affects a space that already exists — not just a newly created
one — say what happens to it. Spaces carried across an upgrade are where most
of this project's bugs have lived.
-->
