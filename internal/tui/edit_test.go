package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/orkcom-tech/contextverse/internal/graph"
)

func stubEditor(t *testing.T, name string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub not portable to windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", name)
	// hostShellAllowed() treats an SSH session without the Model A marker as a
	// place where spawning a program is a shell escape; keep these tests local.
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "")
}

func filesModel(t *testing.T) model {
	t.Helper()
	m := newModel(t.TempDir(), t.TempDir())
	m.ready = true
	m.width, m.height = 100, 40
	m.tab = tabFiles
	m.files = []TrackedFile{
		{Path: "team/deploy.md", Version: "3", Label: "team/deploy.md  v3"},
		{Path: "identity/me.md", Version: "1", Label: "identity/me.md  v1"},
	}
	return m
}

func press(t *testing.T, m model, key string) model {
	t.Helper()
	var km tea.KeyMsg
	switch key {
	case "enter":
		km = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		km = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, _ := m.Update(km)
	out, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return out
}

// Enter on the file list is the reported bug: it used to open version history,
// so a user pressing Enter to edit got a list of numbers instead of a file.
func TestEnterOnFileListOpensEditorPicker(t *testing.T) {
	stubEditor(t, "nano")
	m := press(t, filesModel(t), "enter")

	if !m.edit.picking {
		t.Fatalf("picker not open after Enter (flash: %q)", m.snap.LastMsg)
	}
	if m.edit.path != "team/deploy.md" {
		t.Errorf("editing %q, want the file under the cursor", m.edit.path)
	}
	if len(m.edit.choices) == 0 {
		t.Error("picker has no editors to choose from")
	}
	if m.fileVerMode {
		t.Error("Enter should no longer drop into version history")
	}
}

func TestEscapeCancelsPicker(t *testing.T) {
	stubEditor(t, "nano")
	m := press(t, press(t, filesModel(t), "enter"), "esc")

	if m.edit.picking {
		t.Error("picker still open after esc")
	}
	if m.edit.pending != nil {
		t.Error("esc left a scratch file behind")
	}
}

func TestPickerCursorMovesWithinBounds(t *testing.T) {
	stubEditor(t, "nano")
	m := press(t, filesModel(t), "enter")

	m = press(t, m, "k") // already at top
	if m.edit.cursor != 0 {
		t.Errorf("cursor = %d at top, want 0", m.edit.cursor)
	}
	m = press(t, m, "j")
	want := 1
	if len(m.edit.choices) == 1 {
		want = 0 // only one editor installed in this environment
	}
	if m.edit.cursor != want {
		t.Errorf("cursor = %d after j, want %d", m.edit.cursor, want)
	}
	// The file-list cursor must not move while the picker owns j/k.
	if m.cursor != 0 {
		t.Errorf("file cursor moved to %d while picker was open", m.cursor)
	}
}

// Version history moved to V when Enter became edit; it must still be reachable.
func TestVOpensVersionHistoryNotThePicker(t *testing.T) {
	stubEditor(t, "nano")
	m := filesModel(t)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("V")})
	m = next.(model)

	if m.edit.picking {
		t.Error("V opened the editor picker; it should open version history")
	}
	if cmd == nil {
		t.Error("V produced no command — version history was not requested")
	}
	if !m.busy {
		t.Error("V should mark the model busy while versions load")
	}
}

// An editor is a shell escape, so it must be refused wherever `!` is refused.
func TestEditRefusedWhereShellEscapeIsRefused(t *testing.T) {
	stubEditor(t, "nano")
	t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 22")
	t.Setenv("CONTEXTVERSE_MODEL_A", "")

	m := press(t, filesModel(t), "enter")
	if m.edit.picking {
		t.Fatal("picker opened under Model B — an editor there is a shell escape")
	}
	if m.snap.LastMsg == "" {
		t.Error("refusal was silent; the user needs to be told why")
	}
}

func TestEditorsForPutsRememberedChoiceFirst(t *testing.T) {
	stubEditor(t, "nano")
	// vim is also installed, but nano is remembered.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vim"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EDITOR", "vim")

	got := editorsFor("nano")
	if len(got) == 0 {
		t.Fatal("no editors detected")
	}
	if got[0].ID != "nano" {
		t.Errorf("first choice = %q, want the remembered nano", got[0].ID)
	}
}

// A user who opens the TUI on an empty directory must be told what to do, not
// shown a blank shell with no explanation.
func TestEmptySpaceOffersSetup(t *testing.T) {
	m := newModel(t.TempDir(), t.TempDir())
	m.ready = true
	m.width, m.height = 100, 40

	detail := m.spaceDetail()
	if !strings.Contains(detail, "contextd init") {
		t.Errorf("empty space panel does not point at setup:\n%s", detail)
	}
	if !strings.Contains(detail, "No context space here yet") {
		t.Errorf("empty space panel does not say the space is missing:\n%s", detail)
	}
}

// The Help tab is the only in-app documentation, so a key that does not exist,
// or a tab number that moved, is worse than no help at all.
func TestHelpTabMatchesRealKeys(t *testing.T) {
	m := filesModel(t)
	m.tab = tabHelp
	help := m.renderBody(100, 40)

	for _, want := range []string{
		"enter", "V", "v", "R",
		"tab 3 · Files",
		"tab 4 · Plugins",
		"contextd file edit",
		"contextd --help",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help tab is missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "3  plugins") {
		t.Error("help still calls tab 3 the plugins tab; it is Files")
	}
}

// The Graph tab is navigation: the cursor and the document it would walk to
// must not drift apart, and a row that points nowhere must not be walkable.
func TestGraphTabRowsCarryTheirTarget(t *testing.T) {
	m := filesModel(t)
	m.tab = tabGraph
	m.graph = &graph.Graph{
		Nodes: []graph.Node{
			{Path: "context-entry.md", Title: "Entry", OutDegree: 1},
			{Path: "team/principles.md", Title: "Principles", InDegree: 1},
		},
		Edges: []graph.Edge{
			{From: "context-entry.md", To: "team/principles.md", Line: 3},
		},
	}
	m.graph.Reindex()

	rows := m.graphRows()
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want one per document", len(rows))
	}
	if rows[0].target != "context-entry.md" {
		t.Errorf("row 0 targets %q", rows[0].target)
	}

	// Enter opens the selected document's connections.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.graphFocus != "context-entry.md" {
		t.Fatalf("focus = %q after enter, want the selected document", m.graphFocus)
	}

	rows = m.graphRows()
	if len(rows) != 1 || rows[0].target != "team/principles.md" {
		t.Fatalf("neighbourhood rows = %+v, want the one outbound edge", rows)
	}

	// esc backs out to the whole graph rather than leaving the tab.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.graphFocus != "" {
		t.Error("esc did not back out of the focused document")
	}
	if m.tab != tabGraph {
		t.Error("esc left the Graph tab instead of backing out one level")
	}
}

// A broken or code edge is not somewhere to walk to; offering it would send the
// user to a document that does not exist.
func TestGraphTabWillNotWalkToNothing(t *testing.T) {
	m := filesModel(t)
	m.tab = tabGraph
	m.graphFocus = "a.md"
	m.graph = &graph.Graph{
		Nodes: []graph.Node{{Path: "a.md"}},
		Edges: []graph.Edge{
			{From: "a.md", To: "gone.md", Line: 1, Broken: true},
			{From: "a.md", To: "./x.sh", Line: 2, Code: true},
		},
	}
	m.graph.Reindex()

	for _, r := range m.graphRows() {
		if r.target != "" {
			t.Errorf("row %q offers a walk to %q, but it points at nothing reachable", r.label, r.target)
		}
	}
}
