package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/testspace"
)

// Golden snapshots of what the TUI actually draws.
//
// The three tabs that contradicted each other were each individually
// defensible: every function returned what its own test expected. What nobody
// looked at was the screen, where "8 files" on one tab and "(no tracked files)"
// on another sat two keystrokes apart. A rendered snapshot puts that in the
// diff, in the same words the user reads.
//
// Update with: go test ./internal/tui -update

var update = flag.Bool("update", false, "rewrite the golden files")

// unstable strips what legitimately differs between runs. Paths are not in this
// list — they are fixed before rendering instead; see settle.
var unstable = []*regexp.Regexp{
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2})?`), // timestamps
	regexp.MustCompile(`\d+(\.\d+)?(ms|µs|s)\b`),              // durations
}

func snapshot(t *testing.T, name, got string) {
	t.Helper()
	for _, re := range unstable {
		got = re.ReplaceAllString(got, "<redacted>")
	}
	// Trailing padding is invisible on screen and noisy in a diff.
	lines := strings.Split(got, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	got = strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"

	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file for %s; run: go test ./internal/tui -update\n\ngot:\n%s", name, got)
	}
	if string(want) != got {
		t.Errorf("%s changed.\n\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// upgradedModel is the TUI as it comes up against a space carried across an
// upgrade — the state every one of these bugs lived in.
func upgradedModel(t *testing.T) model {
	t.Helper()
	root := testspace.Legacy(t)

	m := newModel(root, t.TempDir())
	m.ready = true
	m.width, m.height = 100, 32

	m.snap = LoadSnapshot(root)
	files, err := listSpaceFiles(legacyFileLog(t, root), root)
	if err != nil {
		t.Fatal(err)
	}
	m.files = files
	if msg, ok := loadGraphCmd(root)().(graphLoadedMsg); ok && msg.err == nil {
		m.graph = msg.g
	}
	return settle(m)
}

// settle replaces everything that reflects the machine rather than the space,
// before the panels are laid out.
//
// Redacting the rendered text afterwards is too late. A temp directory is short
// on Linux and long on macOS, so the panel wraps it onto a second line on one
// platform and not the other — the line count differs, every following line
// shifts, and no amount of substitution on the output can put that back. The
// snapshot then fails on a CI runner for having a different name for /tmp,
// which teaches everyone to ignore it.
func settle(m model) model {
	m.spaceRoot = "/space"
	m.cwd = "/work"
	m.snap.SpaceRoot = "/space"
	// Plugin detection reads the machine: a laptop with Cursor installed and a
	// CI runner without it would disagree forever.
	for i := range m.snap.Plugins {
		m.snap.Plugins[i].Detected = false
		m.snap.Plugins[i].How = ""
	}
	return m
}

func TestGoldenTabs(t *testing.T) {
	stubEditor(t, "nano")

	for _, tc := range []struct {
		name string
		tab  clientTab
	}{
		{"space", tabSpace},
		{"files", tabFiles},
		{"graph", tabGraph},
		{"help", tabHelp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := upgradedModel(t)
			m.tab = tc.tab
			snapshot(t, "upgraded-"+tc.name, m.View())
		})
	}
}

// The snapshots are only worth keeping if they would show the contradiction.
// This asserts the two tabs tell the same story on the same space, in the text
// a person reads rather than in the structures behind it.
func TestRenderedTabsDoNotContradictEachOther(t *testing.T) {
	stubEditor(t, "nano")
	m := upgradedModel(t)

	m.tab = tabFiles
	files := m.View()
	for _, phrase := range []string{"(no tracked files)", "(no files)", "Empty space"} {
		if strings.Contains(files, phrase) {
			t.Errorf("the Files tab renders %q over a space with %d documents",
				phrase, len(testspace.LegacyDocuments()))
		}
	}
	if !strings.Contains(files, "identity/me.md") {
		t.Error("the Files tab does not name a document that is in the space")
	}

	m.tab = tabSpace
	space := m.View()
	if strings.Contains(space, "no context") || strings.Contains(space, "0 files") {
		t.Errorf("the Space tab calls the space empty while the Files tab lists documents:\n%s", space)
	}
}

// The snapshots failed on Linux and passed on macOS, because a temp directory
// is short on one and long on the other: the panel wrapped the path onto a
// second line on one platform only, every line below it shifted, and the whole
// snapshot differed. A snapshot that depends on where the space happens to live
// fails for reasons nobody caused, and a test that cries wolf gets ignored —
// which costs more than having no test.
//
// This asserts the invariant directly rather than trusting the fix: the same
// space rendered from two different locations must draw the same screen.
func TestRenderDoesNotDependOnWhereTheSpaceLives(t *testing.T) {
	stubEditor(t, "nano")

	deep := filepath.Join(t.TempDir(), "a", "very", "deeply", "nested", "path", "that", "wraps")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", deep)
	long := upgradedModel(t)

	t.Setenv("TMPDIR", "/tmp")
	short := upgradedModel(t)

	for _, tab := range []struct {
		name string
		tab  clientTab
	}{{"space", tabSpace}, {"files", tabFiles}, {"graph", tabGraph}} {
		long.tab, short.tab = tab.tab, tab.tab
		if long.View() != short.View() {
			t.Errorf("the %s tab draws differently depending on the space's path\n\n--- deep ---\n%s\n--- shallow ---\n%s",
				tab.name, long.View(), short.View())
		}
	}
}
