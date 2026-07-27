package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The cache is what makes "derived on read" affordable.
//
// Deriving the graph is a walk plus a parse — tens of milliseconds for a few
// hundred documents — but it happens on `activate`, which sits at the start of
// every AI session. Doing it twice for an unchanged space is waste, and asking
// the user to run a build command would guarantee the map goes stale, which is
// the failure this package exists to end.

const cacheFile = ".contextverse/graph.json"

// fingerprint summarises the file set: every path with its size and modification
// time. Content hashing would be more exact and would mean reading every file
// to decide whether we need to read every file.
func fingerprint(root string) string {
	h := sha256.New()
	var entries []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if skipFile(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, fmt.Sprintf("%s|%d|%d", rel, info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	sort.Strings(entries)
	for _, e := range entries {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Load returns the graph for a space, deriving it only when the cached one no
// longer matches what is on disk.
func Load(opts Options) (*Graph, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("space root is required")
	}
	want := fingerprint(opts.Root)

	if g, err := readCache(opts.Root); err == nil && g.Fingerprint == want {
		// Anchors live in config rather than in the cache, so a checkout that
		// moved since the graph was cached must not be answered from it.
		if sameAnchors(g.Anchors, opts.Anchors) {
			g.reindex()
			return g, nil
		}
	}

	g, err := Build(opts)
	if err != nil {
		return nil, err
	}
	if err := writeCache(opts.Root, g); err != nil {
		// A space that cannot cache is slower, not broken.
		return g, nil
	}
	return g, nil
}

func readCache(root string) (*Graph, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cacheFile)))
	if err != nil {
		return nil, err
	}
	var g Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func writeCache(root string, g *Graph) error {
	path := filepath.Join(root, filepath.FromSlash(cacheFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// reindex rebuilds the unexported lookup a decoded graph does not carry.
func (g *Graph) reindex() {
	g.byPath = make(map[string]int, len(g.Nodes))
	for i, n := range g.Nodes {
		g.byPath[n.Path] = i
	}
}

func sameAnchors(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Age reports how long ago the graph was derived, for surfaces that want to say
// so rather than imply the map is live.
func (g *Graph) Age(now time.Time) time.Duration {
	if g.BuiltAt.IsZero() {
		return 0
	}
	return now.Sub(g.BuiltAt)
}

// Summary is a one-line description used by status output.
func (g *Graph) Summary() string {
	parts := []string{
		fmt.Sprintf("%d nodes", len(g.Nodes)),
		fmt.Sprintf("%d links", len(g.Edges)-len(g.Broken)),
	}
	if len(g.Orphans) > 0 {
		parts = append(parts, fmt.Sprintf("%d orphans", len(g.Orphans)))
	}
	if len(g.Broken) > 0 {
		parts = append(parts, fmt.Sprintf("%d broken", len(g.Broken)))
	}
	return strings.Join(parts, " · ")
}
