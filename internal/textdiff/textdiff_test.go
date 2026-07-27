package textdiff

import (
	"strings"
	"testing"
)

func TestIdenticalTextProducesNoDiff(t *testing.T) {
	const s = "one\ntwo\nthree\n"
	if got := Unified(s, s, "a", "b", 3); got != "" {
		t.Errorf("identical input produced a diff:\n%s", got)
	}
	if a, r := Stat(s, s); a != 0 || r != 0 {
		t.Errorf("Stat = +%d -%d, want 0/0", a, r)
	}
}

func TestReplacedLine(t *testing.T) {
	old := "# Principles\n\nWe deploy on Fridays.\nWe write tests.\n"
	new := "# Principles\n\nWe never deploy on Fridays.\nWe write tests.\n"

	added, removed := Stat(old, new)
	if added != 1 || removed != 1 {
		t.Fatalf("Stat = +%d -%d, want +1 -1", added, removed)
	}

	out := Unified(old, new, "old", "new", 3)
	if !strings.Contains(out, "-We deploy on Fridays.") {
		t.Errorf("removed line missing:\n%s", out)
	}
	if !strings.Contains(out, "+We never deploy on Fridays.") {
		t.Errorf("added line missing:\n%s", out)
	}
	// Context must be carried, not just the changed lines.
	if !strings.Contains(out, " # Principles") {
		t.Errorf("context line missing:\n%s", out)
	}
}

func TestAppendAndDelete(t *testing.T) {
	if a, r := Stat("a\nb\n", "a\nb\nc\n"); a != 1 || r != 0 {
		t.Errorf("append = +%d -%d, want +1 -0", a, r)
	}
	if a, r := Stat("a\nb\nc\n", "a\nc\n"); a != 0 || r != 1 {
		t.Errorf("delete = +%d -%d, want +0 -1", a, r)
	}
}

func TestEmptySides(t *testing.T) {
	if a, r := Stat("", "hello\n"); a != 1 || r != 0 {
		t.Errorf("creation = +%d -%d, want +1 -0", a, r)
	}
	if a, r := Stat("hello\n", ""); a != 0 || r != 1 {
		t.Errorf("emptying = +%d -%d, want +0 -1", a, r)
	}
}

// A trailing newline terminates the last line; treating it as starting an empty
// one reports a phantom change on files that differ only by their final byte.
func TestTrailingNewlineIsNotAPhantomLine(t *testing.T) {
	if a, r := Stat("a\nb\n", "a\nb\n"); a != 0 || r != 0 {
		t.Errorf("Stat = +%d -%d, want 0/0", a, r)
	}
	if lines := splitLines("a\nb\n"); len(lines) != 2 {
		t.Errorf("splitLines = %q, want 2 lines", lines)
	}
	if lines := splitLines("a\nb"); len(lines) != 2 {
		t.Errorf("splitLines without trailing newline = %q, want 2 lines", lines)
	}
}

func TestCRLFIsNormalised(t *testing.T) {
	if a, r := Stat("a\r\nb\r\n", "a\nb\n"); a != 0 || r != 0 {
		t.Errorf("CRLF vs LF reported as +%d -%d; line endings alone are not a change", a, r)
	}
}

// Hunk headers drive every diff viewer; wrong line numbers send a reader to the
// wrong part of the file.
func TestHunkHeaderLineNumbers(t *testing.T) {
	old := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	new := "1\n2\n3\n4\n5\nSIX\n7\n8\n9\n10\n"

	hunks := Hunks(old, new, 2)
	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 4 || h.NewStart != 4 {
		t.Errorf("hunk starts at old=%d new=%d, want 4/4 (2 lines of context before line 6)", h.OldStart, h.NewStart)
	}
	if h.OldLines != 5 || h.NewLines != 5 {
		t.Errorf("hunk covers old=%d new=%d lines, want 5/5", h.OldLines, h.NewLines)
	}
}

// Distant edits belong in separate hunks; adjacent ones should not fragment.
func TestSeparateHunksForDistantChanges(t *testing.T) {
	var oldB, newB strings.Builder
	for i := 1; i <= 40; i++ {
		oldB.WriteString("line\n")
		if i == 2 {
			newB.WriteString("CHANGED-TOP\n")
			continue
		}
		if i == 38 {
			newB.WriteString("CHANGED-BOTTOM\n")
			continue
		}
		newB.WriteString("line\n")
	}
	hunks := Hunks(oldB.String(), newB.String(), 3)
	if len(hunks) != 2 {
		t.Fatalf("got %d hunks for two distant edits, want 2", len(hunks))
	}
}
