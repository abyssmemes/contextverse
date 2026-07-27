// Package textdiff produces a unified diff between two versions of a file.
//
// Written rather than pulled in: contextd ships as a single static binary, a
// line diff is a well-understood ~100 lines, and the only diff library already
// in the module graph (via go-git) is character-oriented, which is the wrong
// shape for showing what changed in a document.
package textdiff

import (
	"fmt"
	"strings"
)

// Op is one edit in a diff.
type Op int

const (
	Equal Op = iota
	Insert
	Delete
)

// Line is a single output line with its operation.
type Line struct {
	Op   Op
	Text string
}

// Hunk is a contiguous run of changes plus its surrounding context.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines              []Line
}

// Unified renders a unified diff. context is the number of unchanged lines kept
// around each change; labels name the two sides.
func Unified(oldText, newText, oldLabel, newLabel string, context int) string {
	if context < 0 {
		context = 3
	}
	hunks := Hunks(oldText, newText, context)
	if len(hunks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", oldLabel)
	fmt.Fprintf(&b, "+++ %s\n", newLabel)
	for _, h := range hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			switch l.Op {
			case Insert:
				fmt.Fprintf(&b, "+%s\n", l.Text)
			case Delete:
				fmt.Fprintf(&b, "-%s\n", l.Text)
			default:
				fmt.Fprintf(&b, " %s\n", l.Text)
			}
		}
	}
	return b.String()
}

// Hunks computes the changed regions between two texts.
func Hunks(oldText, newText string, context int) []Hunk {
	a := splitLines(oldText)
	b := splitLines(newText)
	script := diffLines(a, b)

	// Nothing changed: no hunks, so callers can report "identical" rather than
	// printing an empty diff header.
	changed := false
	for _, l := range script {
		if l.Op != Equal {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}

	var hunks []Hunk
	i := 0
	oldLine, newLine := 1, 1
	for i < len(script) {
		if script[i].Op == Equal {
			oldLine++
			newLine++
			i++
			continue
		}

		// Walk back over up to `context` equal lines to open the hunk.
		start := i
		back := 0
		for start > 0 && script[start-1].Op == Equal && back < context {
			start--
			back++
		}

		h := Hunk{OldStart: oldLine - back, NewStart: newLine - back}
		if h.OldStart < 1 {
			h.OldStart = 1
		}
		if h.NewStart < 1 {
			h.NewStart = 1
		}

		// Consume changes, allowing runs of up to 2*context equal lines to be
		// absorbed so adjacent edits share one hunk instead of fragmenting.
		j := start
		lastChange := i
		for j < len(script) {
			if script[j].Op != Equal {
				lastChange = j
				j++
				continue
			}
			gap := 0
			k := j
			for k < len(script) && script[k].Op == Equal {
				gap++
				k++
			}
			if k >= len(script) || gap > 2*context {
				break
			}
			j = k
		}
		end := lastChange + context + 1
		if end > len(script) {
			end = len(script)
		}

		for _, l := range script[start:end] {
			h.Lines = append(h.Lines, l)
			switch l.Op {
			case Equal:
				h.OldLines++
				h.NewLines++
			case Delete:
				h.OldLines++
			case Insert:
				h.NewLines++
			}
		}
		hunks = append(hunks, h)

		// Advance the line counters past everything the hunk consumed.
		for _, l := range script[i:end] {
			switch l.Op {
			case Equal:
				oldLine++
				newLine++
			case Delete:
				oldLine++
			case Insert:
				newLine++
			}
		}
		i = end
	}
	return hunks
}

// diffLines is a longest-common-subsequence diff over whole lines.
//
// Documents are small and the table is only computed once per command, so the
// straightforward O(n·m) dynamic program is the right trade: it is easy to
// verify, and a context space is prose, not a gigabyte of logs.
func diffLines(a, b []string) []Line {
	n, m := len(a), len(b)

	// Trim the common head and tail first; for an edit to one paragraph this
	// collapses the table to almost nothing.
	head := 0
	for head < n && head < m && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < n-head && tail < m-head && a[n-1-tail] == b[m-1-tail] {
		tail++
	}

	midA := a[head : n-tail]
	midB := b[head : m-tail]

	lcs := make([][]int, len(midA)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(midB)+1)
	}
	for i := len(midA) - 1; i >= 0; i-- {
		for j := len(midB) - 1; j >= 0; j-- {
			if midA[i] == midB[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	out := make([]Line, 0, n+m)
	for _, l := range a[:head] {
		out = append(out, Line{Op: Equal, Text: l})
	}
	i, j := 0, 0
	for i < len(midA) && j < len(midB) {
		switch {
		case midA[i] == midB[j]:
			out = append(out, Line{Op: Equal, Text: midA[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Line{Op: Delete, Text: midA[i]})
			i++
		default:
			out = append(out, Line{Op: Insert, Text: midB[j]})
			j++
		}
	}
	for ; i < len(midA); i++ {
		out = append(out, Line{Op: Delete, Text: midA[i]})
	}
	for ; j < len(midB); j++ {
		out = append(out, Line{Op: Insert, Text: midB[j]})
	}
	for _, l := range a[n-tail:] {
		out = append(out, Line{Op: Equal, Text: l})
	}
	return out
}

// Stat counts insertions and deletions.
func Stat(oldText, newText string) (added, removed int) {
	for _, l := range diffLines(splitLines(oldText), splitLines(newText)) {
		switch l.Op {
		case Insert:
			added++
		case Delete:
			removed++
		}
	}
	return added, removed
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	// A trailing newline ends the last line; it does not start an empty one.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
