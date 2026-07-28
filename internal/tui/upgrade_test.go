package tui

import (
	"strings"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/storage"
	"github.com/orkcom-tech/contextverse/internal/testspace"
)

// These run against a space carried across an upgrade rather than one this
// build just created. Every bug they cover shipped, was found by a person on a
// second laptop, and was unreachable from a fresh space — so a passing suite
// meant nothing.

func legacyFileLog(t *testing.T, root string) *storage.FileLog {
	t.Helper()
	fl, err := openClientFileLog(root)
	if err != nil {
		t.Fatalf("open file log for an upgraded space: %v", err)
	}
	return fl
}

// The shipped bug: the Files tab listed the version log, which on an upgraded
// space knows nothing, so it showed "(no tracked files)" over a space with
// eight documents in it — nothing to select, nothing to open, and no way to
// tell which of the tabs was lying.
func TestFilesTabSeesAnUpgradedSpace(t *testing.T) {
	root := testspace.Legacy(t)
	files, err := listSpaceFiles(legacyFileLog(t, root), root)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = true
	}
	for _, want := range testspace.LegacyDocuments() {
		if want == "config.yaml" {
			continue
		}
		if !got[want] {
			t.Errorf("%s is in the space but missing from the Files tab", want)
		}
	}
	if len(files) == 0 {
		t.Fatal("Files tab is empty on a space full of documents")
	}

	// Untracked is a state to show, not a reason to hide: the row has to be
	// there so Enter has something to open.
	for _, f := range files {
		if !f.Untracked {
			t.Errorf("%s reported as versioned, but this space has no version log", f.Path)
		}
		if !strings.Contains(f.Label, f.Path) {
			t.Errorf("row label %q does not name its file", f.Label)
		}
	}
}

// The contradiction the screenshots showed: Space counted the directory, Files
// read the version log, and the two disagreed in public.
func TestSpaceAndFilesTabsAgree(t *testing.T) {
	root := testspace.Legacy(t)

	snap := LoadSnapshot(root)
	layerTotal := 0
	for _, l := range snap.Layers {
		layerTotal += l.Files
	}
	if layerTotal == 0 {
		t.Fatal("Space tab counted nothing in a space with documents")
	}

	files, err := listSpaceFiles(legacyFileLog(t, root), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("Files tab is empty while the Space tab counts documents — the two tabs contradict each other")
	}

	// Every file the layers counted must be reachable from the Files tab. The
	// reverse is not required: Files also lists root documents, which are not
	// inside any layer.
	inFiles := map[string]bool{}
	for _, f := range files {
		inFiles[f.Path] = true
	}
	for _, l := range snap.Layers {
		found := 0
		for p := range inFiles {
			if strings.HasPrefix(p, l.Name+"/") {
				found++
			}
		}
		if found != l.Files {
			t.Errorf("Space tab says %s has %d files, Files tab shows %d", l.Name, l.Files, found)
		}
	}
}

// An empty Files tab was the reason "how do I open a file" had no answer: Enter
// works, there was simply never a row under the cursor.
func TestEnterHasSomethingToOpenOnAnUpgradedSpace(t *testing.T) {
	stubEditor(t, "nano")
	root := testspace.Legacy(t)

	files, err := listSpaceFiles(legacyFileLog(t, root), root)
	if err != nil {
		t.Fatal(err)
	}

	m := newModel(root, t.TempDir())
	m.ready = true
	m.width, m.height = 100, 40
	m.tab = tabFiles
	m.files = files

	m = press(t, m, "enter")
	if !m.edit.picking {
		t.Fatalf("Enter opened nothing on an upgraded space (flash: %q)", m.snap.LastMsg)
	}
	if m.edit.path == "" {
		t.Error("the editor was opened without a file")
	}
}

// The Graph tab derives from the working tree, so it must see the same space —
// a third surface disagreeing would be the same bug again.
func TestGraphSeesAnUpgradedSpace(t *testing.T) {
	root := testspace.Legacy(t)

	msg := loadGraphCmd(root)()
	loaded, ok := msg.(graphLoadedMsg)
	if !ok {
		t.Fatalf("unexpected message %T", msg)
	}
	if loaded.err != nil {
		t.Fatal(loaded.err)
	}
	if len(loaded.g.Nodes) == 0 {
		t.Fatal("the graph is empty on a space full of documents")
	}

	files, err := listSpaceFiles(legacyFileLog(t, root), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.g.Nodes) != len(files) {
		t.Errorf("graph has %d documents, Files tab has %d — two surfaces, two answers",
			len(loaded.g.Nodes), len(files))
	}
}
