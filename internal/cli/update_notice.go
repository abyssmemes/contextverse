package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/orkcom-tech/contextverse/internal/config"
	"github.com/orkcom-tech/contextverse/internal/prompt"
	"github.com/orkcom-tech/contextverse/internal/selfupdate"
	"github.com/orkcom-tech/contextverse/internal/version"
)

// Where the update notice is allowed to appear.
//
// The check itself is cheap and silent; what makes a version notice hated is
// where it lands. Ranked by damage:
//
//   - It corrupts a protocol. `mcp serve` speaks JSON-RPC over stdio, and a
//     stray line breaks the AI client outright. `daemon run` is a background
//     process with nobody reading it.
//   - It corrupts a parse. --json and --yaml exist to be piped into something.
//   - It is simply not wanted. Scripts, CI, and any non-terminal output.
//
// So the rule is an allowlist of circumstances rather than a blocklist of
// commands: a terminal, a person, no structured output, and not one of the
// commands whose output belongs to a machine.

// quietCommands never print a notice, whatever else is true. Named by their
// full path so a future `contextd mcp inspect` is not silenced by accident.
var quietCommands = map[string]bool{
	"contextd mcp serve":    true, // JSON-RPC on stdio; a line here breaks the client
	"contextd daemon run":   true, // background process, nobody is reading
	"contextd completion":   true, // output is sourced by the shell
	"contextd server start": true, // long-running; the notice would scroll past at boot
}

// maybePrintUpdateNotice writes at most one line to stderr, or nothing.
//
// Called from the root command's PersistentPostRun, which cobra skips when the
// command failed — somebody debugging a broken command does not also need to
// hear about a release.
func maybePrintUpdateNotice(cmd *cobra.Command) {
	if !updateNoticeAllowed(cmd) {
		return
	}
	checker := &selfupdate.Checker{Current: version.Version}
	if notice := checker.Notice(cmd.Context()); notice != "" {
		// stderr, always: stdout may be in a pipe even when it looks like a
		// terminal to us, and this is not part of any command's answer.
		fmt.Fprintln(os.Stderr, notice)
	}
}

func updateNoticeAllowed(cmd *cobra.Command) bool {
	if cmd == nil || quietCommands[cmd.CommandPath()] {
		return false
	}
	// Structured output is somebody's input. structuredOutput already answers
	// this for the whole CLI — it was written for exactly this and had no
	// callers, so hand-rolling the flag check here would have been a second
	// answer to the same question, free to drift from the first.
	if structuredOutput() {
		return false
	}
	// A person at a terminal, not a pipe, a cron job or a build.
	if !prompt.Interactive() {
		return false
	}
	if isCI() {
		return false
	}
	if noUpdateCheckConfigured() {
		return false
	}
	return true
}

// isCI recognises the usual markers. Every one of these means the output is
// going into a log nobody reads until something breaks.
func isCI() bool {
	for _, key := range []string{"CI", "CONTINUOUS_INTEGRATION", "BUILD_NUMBER", "GITHUB_ACTIONS", "GITLAB_CI"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

// noUpdateCheckConfigured reads the persistent off switch. The environment
// variable is handled inside the checker; this is the setting somebody makes
// once instead of editing their shell profile.
func noUpdateCheckConfigured() bool {
	root, err := resolveSpaceRoot()
	if err != nil || !config.Exists(root) {
		return false
	}
	cfg, err := config.Load(root)
	if err != nil {
		return false
	}
	return cfg.NoUpdateCheck
}
