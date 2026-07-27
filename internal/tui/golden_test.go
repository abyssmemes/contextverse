package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/abyssmemes/contextverse/internal/testspace"
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

// unstable strips what legitimately differs between machines and runs, so the
// snapshot fails for changed output rather than for a changed temp directory.
var unstable = []*regexp.Regexp{
	regexp.MustCompile(`/(var|private|tmp)/[^\s│]*`), // temp paths
	// The panel wraps long paths, so the tail of a temp directory arrives on
	// its own line with the leading slash already gone.
	regexp.MustCompile(`[A-Za-z]*\d{6,}/\d{3}[^\s│]*`),
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2})?`), // timestamps
	regexp.MustCompile(`\d+(\.\d+)?(ms|µs|s)\b`),              // durations
	// Plugin detection reads the machine, not the space: a laptop with Cursor
	// installed and a CI runner without it would disagree forever.
	regexp.MustCompile(`\d+ detected / \d+ known`),
}

var manySpaces = regexp.MustCompile(`  +`)

func snapshot(t *testing.T, name, got string) {
	t.Helper()
	for _, re := range unstable {
		got = re.ReplaceAllString(got, "<redacted>")
	}
	lines := strings.Split(got, "\n")
	for i := range lines {
		// A redacted run rarely has the same width as what it replaced, so the
		// panel's padding to the closing border shifts with the length of a
		// random temp path. Collapse the spacing on those lines: their layout
		// is an artefact of the redaction, not something worth asserting.
		if strings.Contains(lines[i], "<redacted>") {
			lines[i] = manySpaces.ReplaceAllString(lines[i], " ")
		}
		// Trailing padding is invisible on screen and noisy in a diff.
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
