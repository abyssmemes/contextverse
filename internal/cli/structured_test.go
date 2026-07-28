package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/abyssmemes/contextverse/internal/testspace"
)

// Structured output exists so a script sees what a person sees. The failure
// mode is not a malformed field — it is the two drifting: a filter applied in
// one renderer and not the other, so `--json` quietly returns a different set
// than the table. Nothing catches that except comparing them.

// runJSON runs a command twice, once human and once as JSON, and returns both.
func runBoth(t *testing.T, args ...string) (human string, raw string) {
	t.Helper()
	human, err := run(t, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, human)
	}
	raw, err = run(t, append(args, "--json")...)
	if err != nil {
		t.Fatalf("%v --json: %v\n%s", args, err, raw)
	}
	return human, raw
}

// Deliberately against an upgraded space, not a fresh one. On a space this
// build just created every file is tracked, so a renderer that silently drops
// untracked files produces identical output and the test proves nothing — the
// first version of this test was written that way and passed while `--json`
// was hiding half the space.
func TestJSONAndTableDescribeTheSameFiles(t *testing.T) {
	dir := testspace.Legacy(t)

	human, raw := runBoth(t, "--dir", dir, "file", "list")

	var entries []FileEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		t.Fatalf("file list --json is not valid JSON: %v\n%s", err, raw)
	}
	if len(entries) == 0 {
		t.Fatal("file list --json returned nothing for a space with files")
	}

	humanLines := 0
	for _, l := range strings.Split(strings.TrimSpace(human), "\n") {
		if strings.TrimSpace(l) != "" {
			humanLines++
		}
	}
	if humanLines != len(entries) {
		t.Errorf("the table has %d rows and --json has %d entries; one of them is filtering",
			humanLines, len(entries))
	}
	for _, e := range entries {
		if !strings.Contains(human, e.Path) {
			t.Errorf("%s is in --json but not in the table", e.Path)
		}
	}
}

func TestJSONAndTableDescribeTheSamePlugins(t *testing.T) {
	human, raw := runBoth(t, "plugin", "list", "--offline")

	var entries []PluginEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		t.Fatalf("plugin list --json is not valid JSON: %v\n%s", err, raw)
	}
	if len(entries) == 0 {
		t.Fatal("plugin list --json returned nothing; the embedded catalog is never empty")
	}
	for _, e := range entries {
		if !strings.Contains(human, e.ID) {
			t.Errorf("%s is in --json but not in the table", e.ID)
		}
		// Detection is a claim about this machine, and the table marks it. If
		// one says detected and the other does not, a script and a person
		// reading the same command disagree about their own setup.
		marked := strings.Contains(human, "detected("+e.How+")") && e.How != ""
		if e.Detected != marked {
			t.Errorf("%s: --json says detected=%v, the table says %v", e.ID, e.Detected, marked)
		}
	}
}

// An empty result is where the two most easily part company: the table prints a
// parenthetical and JSON has to be an empty array, not null and not the word
// "(no snapshots)".
func TestEmptyResultsAreStillValidJSON(t *testing.T) {
	dir := t.TempDir() + "/space"
	if _, err := run(t, "--dir", dir, "init", "solo", "--name", "A", "--role", "B"); err != nil {
		t.Fatal(err)
	}

	human, raw := runBoth(t, "--dir", dir, "history", "list")
	if !strings.Contains(human, "(no snapshots)") {
		t.Fatalf("expected the empty-state line, got:\n%s", human)
	}

	var entries []SnapshotEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		t.Fatalf("the empty case does not produce valid JSON: %v\n%s", err, raw)
	}
	if len(entries) != 0 {
		t.Errorf("expected an empty array, got %d entries", len(entries))
	}
	if strings.Contains(raw, "no snapshots") {
		t.Error("the human empty-state line leaked into the structured output")
	}
}
