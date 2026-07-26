package editor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeBin puts an executable named name on PATH for the duration of the test.
func fakeBin(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub not portable to windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return p
}

// A GUI editor that returns immediately would let the caller read back the
// unedited body and silently throw the user's work away, so the wait flag must
// be present whether it comes from the table or from $EDITOR.
func TestGUIEditorAlwaysWaits(t *testing.T) {
	fakeBin(t, "code")

	ed, err := Lookup("code")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if ed.Terminal {
		t.Error("code should not be marked as a terminal editor")
	}
	if !hasArg(ed.Args, "--wait") {
		t.Errorf("Lookup(code) args = %v, want --wait", ed.Args)
	}

	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code")
	env, ok := FromEnvironment()
	if !ok {
		t.Fatal("FromEnvironment: not resolved")
	}
	if !hasArg(env.Args, "--wait") {
		t.Errorf("$EDITOR=code args = %v, want --wait added", env.Args)
	}
	if env.Terminal {
		t.Error("$EDITOR=code should be classified as GUI")
	}
}

func TestFromEnvironmentKeepsExplicitArgs(t *testing.T) {
	fakeBin(t, "emacs")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "emacs -nw")

	ed, ok := FromEnvironment()
	if !ok {
		t.Fatal("FromEnvironment: not resolved")
	}
	if len(ed.Args) != 1 || ed.Args[0] != "-nw" {
		t.Errorf("args = %v, want [-nw]", ed.Args)
	}
	if !ed.FromEnv {
		t.Error("FromEnv should be set")
	}
}

func TestVisualBeatsEditor(t *testing.T) {
	fakeBin(t, "nano")
	fakeBin(t, "vim")
	t.Setenv("VISUAL", "vim")
	t.Setenv("EDITOR", "nano")

	ed, ok := FromEnvironment()
	if !ok {
		t.Fatal("FromEnvironment: not resolved")
	}
	if ed.ID != "vim" {
		t.Errorf("ID = %q, want vim ($VISUAL wins)", ed.ID)
	}
}

func TestDetectPutsEnvironmentFirst(t *testing.T) {
	fakeBin(t, "nano")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano")

	found := Detect()
	if len(found) == 0 {
		t.Fatal("Detect returned nothing")
	}
	if !found[0].FromEnv {
		t.Errorf("first entry = %+v, want the $EDITOR one", found[0])
	}
	// The same binary must not also appear as a plain table entry.
	seen := 0
	for _, e := range found {
		if e.Bin == found[0].Bin {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("binary listed %d times, want 1", seen)
	}
}

func TestLookupUnknownEditorFails(t *testing.T) {
	if _, err := Lookup("definitely-not-an-editor-xyz"); err == nil {
		t.Error("expected an error for a binary that is not installed")
	}
}

// Session must report changed=false when the editor saved nothing, so callers
// can skip writing a version identical to the current one.
func TestSessionDetectsNoChange(t *testing.T) {
	bin := fakeBin(t, "noop-editor")
	ed := Editor{ID: "noop", Name: "noop", Bin: bin, Terminal: true}

	out, changed, err := Session(ed, "team/deploy.md", []byte("original\n"))
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if changed {
		t.Error("changed = true, want false for an editor that wrote nothing")
	}
	if string(out) != "original\n" {
		t.Errorf("body = %q, want it returned unchanged", out)
	}
}

func TestPrepareKeepsExtensionForSyntaxHighlighting(t *testing.T) {
	p, err := Prepare(Editor{ID: "x", Bin: "/bin/true"}, "team/deploy.md", []byte("x"))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer p.Cleanup()
	if filepath.Ext(p.Path()) != ".md" {
		t.Errorf("scratch file = %q, want a .md extension", p.Path())
	}
}
