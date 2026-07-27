package graph

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func build(t *testing.T, root string, anchors map[string]string) *Graph {
	t.Helper()
	g, err := Build(Options{Root: root, Anchors: anchors, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func edgeTo(g *Graph, from, to string) (Edge, bool) {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return e, true
		}
	}
	return Edge{}, false
}

// All three syntaxes must resolve: the author's vault uses wikilinks, the
// ContextVerse templates use Markdown links and bare paths, and forcing either
// to be rewritten would guarantee an empty graph.
func TestResolvesAllThreeLinkSyntaxes(t *testing.T) {
	root := space(t, map[string]string{
		"context-entry.md":   "See [[principles]] and [the map](team/space-map.md).\nAlso team/skill-map.md for capability.\n",
		"team/principles.md": "# Principles\n",
		"team/space-map.md":  "# Space map\n",
		"team/skill-map.md":  "# Skills\n",
	})
	g := build(t, root, nil)

	for _, want := range []string{"team/principles.md", "team/space-map.md", "team/skill-map.md"} {
		e, ok := edgeTo(g, "context-entry.md", want)
		if !ok {
			t.Errorf("no edge to %s", want)
			continue
		}
		if e.Broken {
			t.Errorf("edge to %s marked broken", want)
		}
	}
}

func TestRelativeLinksResolveFromTheirOwnDirectory(t *testing.T) {
	root := space(t, map[string]string{
		"projects/api/map.md": "Rules: [rules](../../team/principles.md)\n",
		"team/principles.md":  "# Principles\n",
	})
	g := build(t, root, nil)

	e, ok := edgeTo(g, "projects/api/map.md", "team/principles.md")
	if !ok || e.Broken {
		t.Fatalf("relative link did not resolve: %+v", g.Edges)
	}
}

func TestBrokenLinkIsReported(t *testing.T) {
	root := space(t, map[string]string{
		"a.md": "Gone: [missing](team/nope.md)\n",
	})
	g := build(t, root, nil)

	if len(g.Broken) != 1 {
		t.Fatalf("got %d broken edges, want 1: %+v", len(g.Broken), g.Edges)
	}
	if g.Broken[0].Line != 1 {
		t.Errorf("broken edge reported at line %d, want 1 — a broken link is only actionable if you can find it", g.Broken[0].Line)
	}
}

// External references are not the space's business and must not be reported as
// dead links; that noise would train people to ignore the report.
func TestExternalReferencesAreIgnored(t *testing.T) {
	root := space(t, map[string]string{
		"a.md": "See [docs](https://example.com/page) and [mail](mailto:x@y.com) and [anchor](#section).\n",
	})
	g := build(t, root, nil)

	if len(g.Edges) != 0 {
		t.Errorf("external references became edges: %+v", g.Edges)
	}
}

// The whole point of the anchor: a runbook naming a script should be checked
// against the project it belongs to.
func TestCodeReferenceCheckedAgainstAnchor(t *testing.T) {
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "scripts", "deploy.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := space(t, map[string]string{
		"projects/api/deploy.md": "Run:\n\n    ./scripts/deploy.sh <service>\n    ./scripts/gone.sh\n",
	})
	g := build(t, root, map[string]string{"api": checkout})

	var live, dead int
	for _, e := range g.Edges {
		if !e.Code {
			continue
		}
		if e.Broken {
			dead++
		} else {
			live++
		}
	}
	if live != 1 || dead != 1 {
		t.Fatalf("code edges: %d live, %d dead, want 1/1: %+v", live, dead, g.Edges)
	}
}

// Without an anchor a code reference cannot be judged. Reporting it as broken
// would accuse the user of a dead link because contextd was never told where
// their code lives.
func TestCodeReferenceWithoutAnchorIsNotCalledBroken(t *testing.T) {
	root := space(t, map[string]string{
		"projects/api/deploy.md": "Run `./scripts/deploy.sh`\n",
	})
	g := build(t, root, nil)

	for _, e := range g.Edges {
		if e.Code && e.Broken {
			t.Errorf("unanchored code reference reported as broken: %+v", e)
		}
	}
}

func TestBacklinksAndOrphans(t *testing.T) {
	root := space(t, map[string]string{
		"context-entry.md":   "Start: [principles](team/principles.md)\n",
		"team/principles.md": "# Principles\n",
		"team/lonely.md":     "# Nobody links here\n",
	})
	g := build(t, root, nil)

	n, ok := g.Node("team/principles.md")
	if !ok {
		t.Fatal("node missing")
	}
	if n.InDegree != 1 {
		t.Errorf("InDegree = %d, want 1", n.InDegree)
	}

	var foundLonely, foundEntry bool
	for _, o := range g.Orphans {
		if o == "team/lonely.md" {
			foundLonely = true
		}
		if o == "context-entry.md" {
			foundEntry = true
		}
	}
	if !foundLonely {
		t.Errorf("unlinked document not reported as an orphan: %v", g.Orphans)
	}
	// context-entry.md is the front door; nothing links to it by design.
	if foundEntry {
		t.Error("context-entry.md reported as an orphan; it is the entry point")
	}

	out, in := g.Neighbours("team/principles.md")
	if len(in) != 1 || len(out) != 0 {
		t.Errorf("Neighbours = %d out / %d in, want 0/1", len(out), len(in))
	}
}

// Rank drives what the AI is shown first, so a well-connected document must
// outrank an unreferenced one, and staleness must cost.
func TestRankPrefersConnectedAndPenalisesStale(t *testing.T) {
	root := space(t, map[string]string{
		"context-entry.md": "[a](hub.md) [b](hub.md)\n",
		"other.md":         "[c](hub.md)\n",
		"hub.md":           "# Hub\n",
		"lonely.md":        "# Lonely\n",
	})
	g := build(t, root, nil)

	hub, _ := g.Node("hub.md")
	lonely, _ := g.Node("lonely.md")
	if hub.Rank <= lonely.Rank {
		t.Errorf("hub rank %.5f is not above unreferenced %.5f", hub.Rank, lonely.Rank)
	}

	stale := space(t, map[string]string{
		"context-entry.md": "[a](hub.md)\n",
		"hub.md":           "---\nlast-validated: 2020-01-01\nstale-after: 30d\n---\n\n# Hub\n",
	})
	sg := build(t, stale, nil)
	sh, _ := sg.Node("hub.md")
	if !sh.Stale {
		t.Fatal("expired document not marked stale")
	}
	fresh, _ := g.Node("hub.md")
	if sh.Rank >= fresh.Rank {
		t.Errorf("stale rank %.5f not penalised against fresh %.5f", sh.Rank, fresh.Rank)
	}
}

func TestFrontmatterIsNotScannedForLinks(t *testing.T) {
	root := space(t, map[string]string{
		"a.md": "---\nowner: team/platform\nlast-validated: 2026-01-01\n---\n\n# A\n",
	})
	g := build(t, root, nil)
	if len(g.Edges) != 0 {
		t.Errorf("frontmatter values became edges: %+v", g.Edges)
	}
}

// The cache exists so `activate` does not re-walk an unchanged space, but it
// must never answer for a space that has changed.
func TestCacheIsInvalidatedByAChangedFile(t *testing.T) {
	root := space(t, map[string]string{
		"context-entry.md": "# Entry\n",
	})
	first, err := Load(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(first.Nodes))
	}

	// Same space: served from cache, same fingerprint.
	again, err := Load(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if again.Fingerprint != first.Fingerprint {
		t.Error("fingerprint changed without the space changing")
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "new.md"), []byte("# New\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := Load(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Nodes) != 2 {
		t.Errorf("got %d nodes after adding a file, want 2 — the cache went stale", len(third.Nodes))
	}
}

// A moved checkout changes whether code references resolve, and anchors live in
// config rather than in the cached graph.
func TestCacheIsInvalidatedByChangedAnchors(t *testing.T) {
	root := space(t, map[string]string{"projects/api/a.md": "run ./x.sh\n"})

	if _, err := Load(Options{Root: root, Anchors: map[string]string{"api": "/one"}}); err != nil {
		t.Fatal(err)
	}
	g, err := Load(Options{Root: root, Anchors: map[string]string{"api": "/two"}})
	if err != nil {
		t.Fatal(err)
	}
	if g.Anchors["api"] != "/two" {
		t.Errorf("anchors = %v, want the cache to have been rebuilt", g.Anchors)
	}
}
