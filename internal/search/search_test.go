package search

import (
	"os"
	"path/filepath"
	"testing"
)

func space(t *testing.T, files map[string]string) string {
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

func TestFindsContentWithLineNumbers(t *testing.T) {
	root := space(t, map[string]string{
		"team/principles.md": "# Principles\n\nWe never deploy on Fridays.\nWe write tests.\n",
	})
	res, err := Search(Options{Root: root, Query: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("got %d matches, want 1: %+v", len(res.Matches), res.Matches)
	}
	m := res.Matches[0]
	if m.Line != 3 {
		t.Errorf("line = %d, want 3", m.Line)
	}
	if m.Text != "We never deploy on Fridays." {
		t.Errorf("text = %q", m.Text)
	}
}

// Searching only contents would miss "where is the file about deploys".
func TestMatchesFilenamesToo(t *testing.T) {
	root := space(t, map[string]string{"team/deploy.md": "nothing relevant inside\n"})
	res, err := Search(Options{Root: root, Query: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || !res.Matches[0].Name {
		t.Fatalf("want one filename match, got %+v", res.Matches)
	}
	if res.Matches[0].Line != 0 {
		t.Errorf("a filename match should carry no line number, got %d", res.Matches[0].Line)
	}
}

func TestCaseInsensitiveByDefault(t *testing.T) {
	root := space(t, map[string]string{"a.md": "ArgoCD deploys everything\n"})

	res, _ := Search(Options{Root: root, Query: "argocd"})
	if len(res.Matches) == 0 {
		t.Error("default search should ignore case")
	}
	res, _ = Search(Options{Root: root, Query: "argocd", CaseSense: true})
	if len(res.Matches) != 0 {
		t.Error("--case-sensitive should not match a different case")
	}
}

func TestRegexAndBadPattern(t *testing.T) {
	root := space(t, map[string]string{"a.md": "v1.2.3 shipped\n"})

	res, err := Search(Options{Root: root, Query: `v\d+\.\d+\.\d+`, Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Errorf("regex found %d matches, want 1", len(res.Matches))
	}
	if _, err := Search(Options{Root: root, Query: "v[", Regex: true}); err == nil {
		t.Error("an invalid pattern should be reported, not silently matched literally")
	}
}

func TestPathGlobNarrows(t *testing.T) {
	root := space(t, map[string]string{
		"team/a.md":     "target\n",
		"projects/b.md": "target\n",
	})
	res, err := Search(Options{Root: root, Query: "target", PathGlob: "team/*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Path != "team/a.md" {
		t.Errorf("glob did not narrow the search: %+v", res.Matches)
	}
}

// contextd's own storage is not the user's context; showing hits from it would
// bury real results under object files.
func TestSkipsInternalDirectories(t *testing.T) {
	root := space(t, map[string]string{
		"notes.md":                     "findme\n",
		".contextverse/objects/x.json": "findme\n",
		".git/config":                  "findme\n",
		"node_modules/pkg/index.js":    "findme\n",
	})
	res, err := Search(Options{Root: root, Query: "findme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Path != "notes.md" {
		t.Errorf("internal directories leaked into results: %+v", res.Matches)
	}
}

func TestLimitTruncates(t *testing.T) {
	body := ""
	for i := 0; i < 50; i++ {
		body += "hit\n"
	}
	root := space(t, map[string]string{"a.md": body})

	res, err := Search(Options{Root: root, Query: "hit", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 10 {
		t.Errorf("got %d matches, want the limit of 10", len(res.Matches))
	}
	if !res.Truncated {
		t.Error("Truncated should tell the caller results were cut off")
	}
}

func TestEmptyQueryIsRejected(t *testing.T) {
	if _, err := Search(Options{Root: t.TempDir(), Query: "   "}); err == nil {
		t.Error("an empty query should be an error, not a match against everything")
	}
}
