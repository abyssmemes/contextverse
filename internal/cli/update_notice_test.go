package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// The check itself is tested in internal/selfupdate. What is tested here is the
// part that decides whether anyone hears it, because that is where a version
// notice stops being helpful and starts being the reason somebody turns it off
// — or worse, the reason an AI client's JSON-RPC stream stops parsing.

func cmdPath(path string) *cobra.Command {
	// cobra derives CommandPath from the tree, so build the tree.
	root := &cobra.Command{Use: "contextd"}
	cur := root
	for _, part := range splitPath(path)[1:] {
		child := &cobra.Command{Use: part}
		cur.AddCommand(child)
		cur = child
	}
	return cur
}

func splitPath(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

// The ones that must never speak, and why.
func TestCommandsWhoseOutputBelongsToAMachineAreSilent(t *testing.T) {
	for _, path := range []string{
		"contextd mcp serve",    // JSON-RPC over stdio; a line breaks the AI client
		"contextd daemon run",   // background process, nobody reading
		"contextd completion",   // output is sourced by the shell
		"contextd server start", // long-running; it would scroll past at boot
	} {
		if !quietCommands[path] {
			t.Errorf("%s is not in the quiet list", path)
		}
		if updateNoticeAllowed(cmdPath(path)) {
			t.Errorf("%s would print an update notice", path)
		}
	}
}

// Structured output is somebody's input; a friendly line in it is a parse error.
func TestStructuredOutputIsSilent(t *testing.T) {
	defer func() { flagJSON, flagYAML = false, false }()

	flagJSON, flagYAML = true, false
	if updateNoticeAllowed(cmdPath("contextd status")) {
		t.Error("--json output would carry an update notice")
	}
	flagJSON, flagYAML = false, true
	if updateNoticeAllowed(cmdPath("contextd status")) {
		t.Error("--yaml output would carry an update notice")
	}
}

func TestCIIsSilent(t *testing.T) {
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILD_NUMBER", "CONTINUOUS_INTEGRATION"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "true")
			if !isCI() {
				t.Errorf("%s set but isCI() is false", key)
			}
			if updateNoticeAllowed(cmdPath("contextd status")) {
				t.Errorf("a notice would be printed under %s", key)
			}
		})
	}
}

// The test process is not a terminal, so an ordinary command is silent here for
// the same reason a piped one is in real use. Worth asserting rather than
// assuming: it is the condition that covers every script nobody thought of.
func TestANonTerminalIsSilent(t *testing.T) {
	if updateNoticeAllowed(cmdPath("contextd status")) {
		t.Error("a notice would be printed with stdout not a terminal")
	}
}

func TestNilCommandIsSilent(t *testing.T) {
	if updateNoticeAllowed(nil) {
		t.Error("a nil command was allowed to print")
	}
}

// A command that is not on the quiet list is only silenced by circumstance, not
// by name — otherwise the feature is off for everyone by accident.
func TestOrdinaryCommandsAreNotBlockedByName(t *testing.T) {
	for _, path := range []string{"contextd status", "contextd pull", "contextd version", "contextd mcp"} {
		if quietCommands[path] {
			t.Errorf("%s is silenced by name; it should only be silenced by context", path)
		}
	}
}
