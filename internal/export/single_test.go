package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func spaceWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A pack that quietly omits your principles is worse than one that says it
// could not find them: you would paste it into a model and never know what the
// model was not told.
func TestSingleNamesWhatItCouldNotFind(t *testing.T) {
	root := spaceWith(t, map[string]string{
		"context-entry.md": "# Entry\n\nread these\n",
		"identity/me.md":   "# Me\n\nEduard\n",
		// team/principles.md, space-index.md and decisions.md deliberately absent
	})

	doc, res, err := Single(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Missing) == 0 {
		t.Fatal("nothing reported missing from a space with three documents absent")
	}
	for _, rel := range res.Missing {
		if !strings.Contains(doc, rel) {
			t.Errorf("%s is missing but the document does not mention it", rel)
		}
	}
	if !strings.Contains(doc, "missing from this space") {
		t.Error("the reader is not told anything was missing")
	}
	if !strings.Contains(doc, "Eduard") {
		t.Error("a document that exists did not make it into the pack")
	}
}

// One h1 at the top, sources demoted underneath it. Without this the pack has
// five competing h1s and reads as five documents rather than one.
func TestSingleKeepsOneTopLevelHeading(t *testing.T) {
	root := spaceWith(t, map[string]string{
		"context-entry.md":   "# Entry\n\n## Sub\n",
		"identity/me.md":     "# Me\n",
		"team/principles.md": "# Principles\n",
		"space-index.md":     "# Index\n",
		"decisions.md":       "# Decisions\n",
	})

	doc, _, err := Single(root, "")
	if err != nil {
		t.Fatal(err)
	}
	h1 := 0
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "# ") {
			h1++
		}
	}
	if h1 != 1 {
		t.Errorf("the pack has %d top-level headings; it should read as one document", h1)
	}
	if !strings.Contains(doc, "### Sub") {
		t.Error("a source's own structure was flattened rather than demoted")
	}
}
