package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abyssmemes/contextverse/internal/testspace"
)

// Tests that drive whole commands rather than the functions inside them.
//
// Every bug in this file shipped because the function was tested and the
// command was not: the flag path worked while the path a person types did not,
// or a confirmation was collected and never passed on. Assertions here are on
// what someone at a terminal gets back.

// run executes contextd as a user would, returning combined output.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// The global flags are package state; a leaked --dir would silently point the
	// next test at the previous test's space.
	t.Cleanup(func() {
		flagSpaceRoot, flagServerDir = "", ""
		flagJSON, flagYAML, flagDebug = false, false, false
	})

	var buf bytes.Buffer
	root := newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// The shipped bug: passing --name and --role still stopped to ask for them
// unless --non-interactive was also given. Supplying a value and then being
// asked for it is the tool not listening.
func TestOneCommandSetupNeedsNoExtraFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "space")

	out, err := run(t, "--dir", dir, "init", "solo",
		"--name", "Eduard", "--role", "DevOps engineer", "--tools", "Go, Terraform")
	if err != nil {
		t.Fatalf("one-command setup failed: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(dir, "identity", "me.md")); err != nil {
		t.Fatalf("space was not created: %v", err)
	}
	me, _ := os.ReadFile(filepath.Join(dir, "identity", "me.md"))
	if !strings.Contains(string(me), "Eduard") {
		t.Error("the name passed on the command line did not reach identity/me.md")
	}
}

// A space created this way must be usable immediately: the version log knowing
// nothing about the files it just wrote is the state that made `file list`
// contradict the directory.
func TestFreshSpaceHasTrackedFilesImmediately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "space")
	if _, err := run(t, "--dir", dir, "init", "solo", "--name", "A", "--role", "B"); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "--dir", dir, "file", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "(no files)") {
		t.Fatalf("a freshly created space reports no files:\n%s", out)
	}
	for _, want := range []string{"identity/me.md", "team/principles.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s missing from file list:\n%s", want, out)
		}
	}
}

// --force did nothing on the path a person actually reaches for after the first
// failure. The command has to overwrite when told to.
func TestForceOverwritesAnExistingSpace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "space")
	if _, err := run(t, "--dir", dir, "init", "solo", "--name", "First", "--role", "R"); err != nil {
		t.Fatal(err)
	}

	// Without --force the command must refuse rather than quietly overwrite.
	if _, err := run(t, "--dir", dir, "init", "solo", "--name", "Second", "--role", "R"); err == nil {
		t.Error("re-initialising without --force silently overwrote an existing space")
	}

	out, err := run(t, "--dir", dir, "init", "solo", "--name", "Second", "--role", "R", "--force")
	if err != nil {
		t.Fatalf("--force did not overwrite: %v\n%s", err, out)
	}
	me, _ := os.ReadFile(filepath.Join(dir, "identity", "me.md"))
	if !strings.Contains(string(me), "Second") {
		t.Error("--force reported success but the space was not rewritten")
	}
}

// The command-level version of the contradiction: a space carried across an
// upgrade must not report itself empty.
func TestUpgradedSpaceIsNotReportedEmpty(t *testing.T) {
	root := testspace.Legacy(t)

	out, err := run(t, "--dir", root, "file", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "(no files)") {
		t.Fatalf("an upgraded space with %d documents reports no files:\n%s",
			len(testspace.LegacyDocuments()), out)
	}

	// status must not claim an empty graph either.
	out, err = run(t, "--dir", root, "status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "graph:      0 nodes") {
		t.Errorf("status reports an empty graph for a space full of documents:\n%s", out)
	}
}

// search reads the working tree, so it is the surface least likely to be fooled
// by an empty log — which makes it the control. If search finds documents and
// file list does not, the two disagree and one of them is wrong.
func TestSearchAndFileListAgreeOnAnUpgradedSpace(t *testing.T) {
	root := testspace.Legacy(t)

	found, err := run(t, "--dir", root, "search", "-l", "principles")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found, "team/principles.md") {
		t.Fatalf("search cannot find a document that is in the space:\n%s", found)
	}

	listed, err := run(t, "--dir", root, "file", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, "team/principles.md") {
		t.Errorf("search finds team/principles.md but file list does not:\n%s", listed)
	}
}
