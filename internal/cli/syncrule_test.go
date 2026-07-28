package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// newServer creates a server in a temp directory. The point of the helper is
// the --server-dir: init server used to ignore it and write to the default,
// which meant a test like this would have edited the developer's real server.
func newServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir() + "/srv"
	if out, err := run(t, "--server-dir", dir, "init", "server", "--non-interactive", "--noui"); err != nil {
		t.Fatalf("init server: %v\n%s", err, out)
	}
	if out, err := run(t, "--server-dir", dir, "space", "create", "mine"); err != nil {
		t.Fatalf("space create: %v\n%s", err, out)
	}
	return dir
}

// The command that creates a server was the one command you could not point
// somewhere else: --server-dir steered everything but it. Silently writing to
// the default instead of the directory you named is how a test, or a person
// trying a second server, damages the one they already had.
func TestInitServerHonoursTheDirectoryYouName(t *testing.T) {
	dir := newServer(t)

	out, err := run(t, "--server-dir", dir, "space", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mine") {
		t.Errorf("the space was not created in the named directory:\n%s", out)
	}
}

// Whether identity travels is a policy: init-only protects a team's shared
// space from one person's me.md, and is exactly wrong for one person syncing
// their own two machines through their own server. Changing it used to mean
// hand-editing meta.yaml, so nobody knew it was possible.
func TestSyncModeCanBeChangedForAPersonalServer(t *testing.T) {
	dir := newServer(t)

	before, err := run(t, "--server-dir", dir, "space", "show", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, "identity/ init-only") {
		t.Fatalf("expected the team-safe default:\n%s", before)
	}

	raw, err := run(t, "--server-dir", dir, "space", "sync", "set", "mine", "identity/", "always", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var change SyncRuleChange
	if err := json.Unmarshal([]byte(raw), &change); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, raw)
	}
	if change.From != "init-only" || change.To != "always" {
		t.Errorf("the change reported %q → %q", change.From, change.To)
	}

	after, err := run(t, "--server-dir", dir, "space", "show", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, "identity/ always") {
		t.Errorf("the rule did not persist:\n%s", after)
	}
}

func TestSyncSetRejectsAModeThatDoesNotExist(t *testing.T) {
	dir := newServer(t)

	if _, err := run(t, "--server-dir", dir, "space", "sync", "set", "mine", "identity/", "sometimes"); err == nil {
		t.Error("an unknown mode was accepted; it would silently fall through to the default")
	}
}
