// Package graph derives a navigable map of a context space from the links its
// documents already contain.
//
// # Why derived rather than built
//
// The space model shipped two hand-maintained maps — `space-index.md`, produced
// from a hardcoded format string whose Dependencies column was a literal em
// dash, and `team/space-map.md`, an ASCII drawing seeded once at init. Both were
// wrong the moment a file was added. Anything with a build step rots the same
// way, so nothing here is built: the graph is derived whenever the space is
// read, and cached against a hash of the files it was derived from.
//
// # Why no model is involved
//
// Extracting relations with an LLM would find edges nobody wrote down, at the
// cost of a graph that changes between runs, cannot be diffed, and is a model's
// opinion about the user's knowledge. This product's whole position is that the
// human curates and the AI reads. So every edge here comes from text a person
// wrote, and points at a line you can go and look at.
package graph

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abyssmemes/contextverse/internal/freshness"
)

// EdgeKind records which syntax produced a reference.
type EdgeKind string

const (
	EdgeWikilink EdgeKind = "wikilink"
	EdgeMarkdown EdgeKind = "markdown"
	EdgePath     EdgeKind = "path"
)

// Node is one document in the space.
type Node struct {
	Path       string    `json:"path" yaml:"path"`
	Title      string    `json:"title,omitempty" yaml:"title,omitempty"`
	Owner      string    `json:"owner,omitempty" yaml:"owner,omitempty"`
	Project    string    `json:"project,omitempty" yaml:"project,omitempty"`
	Stale      bool      `json:"stale,omitempty" yaml:"stale,omitempty"`
	Validated  time.Time `json:"last_validated,omitempty" yaml:"last_validated,omitempty"`
	Importance string    `json:"importance,omitempty" yaml:"importance,omitempty"`
	OutDegree  int       `json:"out_degree" yaml:"out_degree"`
	InDegree   int       `json:"in_degree" yaml:"in_degree"`
	Rank       float64   `json:"rank" yaml:"rank"`
}

// Edge is a reference from one document to a file.
type Edge struct {
	From   string   `json:"from" yaml:"from"`
	To     string   `json:"to" yaml:"to"`
	Raw    string   `json:"raw" yaml:"raw"`
	Line   int      `json:"line" yaml:"line"`
	Kind   EdgeKind `json:"kind" yaml:"kind"`
	Code   bool     `json:"code,omitempty" yaml:"code,omitempty"`
	Broken bool     `json:"broken,omitempty" yaml:"broken,omitempty"`
}

// Graph is the derived map.
type Graph struct {
	Root        string            `json:"root" yaml:"root"`
	Nodes       []Node            `json:"nodes" yaml:"nodes"`
	Edges       []Edge            `json:"edges" yaml:"edges"`
	Orphans     []string          `json:"orphans" yaml:"orphans"`
	Broken      []Edge            `json:"broken" yaml:"broken"`
	Anchors     map[string]string `json:"anchors,omitempty" yaml:"anchors,omitempty"`
	BuiltAt     time.Time         `json:"built_at" yaml:"built_at"`
	Fingerprint string            `json:"fingerprint" yaml:"fingerprint"`

	byPath map[string]int // path → index in Nodes
}

// Options controls derivation.
type Options struct {
	Root    string
	Anchors map[string]string // project → checkout path, from config
	Now     time.Time
}

// Build derives the graph for a space.
func Build(opts Options) (*Graph, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("space root is required")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}

	g := &Graph{
		Root:    opts.Root,
		Anchors: opts.Anchors,
		BuiltAt: opts.Now,
		byPath:  map[string]int{},
	}

	type doc struct {
		rel     string
		content []byte
	}
	var docs []doc

	err := filepath.WalkDir(opts.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(opts.Root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skipFile(rel) {
			return nil
		}

		node := Node{Path: rel, Title: titleFromPath(rel)}
		if strings.HasSuffix(strings.ToLower(rel), ".md") {
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			docs = append(docs, doc{rel: rel, content: raw})
			if m, ok := freshness.Parse(rel, raw, time.Time{}); ok {
				node.Owner = m.Owner
				node.Validated = m.LastValidated
				node.Stale = m.Stale || (m.StaleAfter > 0 && opts.Now.After(m.LastValidated.Add(m.StaleAfter)))
			}
			node.Importance = importanceFromFrontmatter(raw)
			if t := titleFromContent(raw); t != "" {
				node.Title = t
			}
		}
		node.Project = projectOf(rel)
		g.byPath[rel] = len(g.Nodes)
		g.Nodes = append(g.Nodes, node)
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, d := range docs {
		for _, l := range parseLinks(d.content) {
			e := g.resolve(d.rel, l)
			g.Edges = append(g.Edges, e)
		}
	}

	g.finalize()
	g.Fingerprint = fingerprint(opts.Root)
	return g, nil
}

// resolve turns a raw reference into an edge, deciding whether it points inside
// the space or out into a project's code.
func (g *Graph) resolve(from string, l rawLink) Edge {
	e := Edge{From: from, Raw: l.Target, Line: l.Line, Kind: l.Kind}

	// Code references leave the space entirely: they are checked against the
	// project checkout `contextd activate` recorded, which is the only reason
	// the anchor exists.
	if looksLikeCode(l.Target) {
		e.Code = true
		e.To = l.Target
		e.Broken = !g.codeTargetExists(from, l.Target)
		return e
	}

	if target, ok := g.resolveInSpace(from, l); ok {
		e.To = target
		return e
	}
	e.To = l.Target
	e.Broken = true
	return e
}

func (g *Graph) resolveInSpace(from string, l rawLink) (string, bool) {
	target := l.Target

	// A wikilink names a note, not a path — resolve it by basename the way
	// Obsidian does, so [[principles]] finds team/principles.md.
	if l.Kind == EdgeWikilink {
		if p, ok := g.findByName(target); ok {
			return p, true
		}
	}

	candidates := []string{}
	if strings.HasPrefix(target, "/") {
		candidates = append(candidates, strings.TrimPrefix(target, "/"))
	} else {
		candidates = append(candidates,
			path.Clean(path.Join(path.Dir(from), target)), // relative to the document
			path.Clean(target),                            // relative to the space root
		)
	}
	for _, c := range candidates {
		c = strings.TrimPrefix(c, "./")
		if _, ok := g.byPath[c]; ok {
			return c, true
		}
		// A link written without its extension still means the document.
		if path.Ext(c) == "" {
			if _, ok := g.byPath[c+".md"]; ok {
				return c + ".md", true
			}
		}
	}
	if p, ok := g.findByName(target); ok {
		return p, true
	}
	return "", false
}

// findByName matches a bare note name against known documents.
func (g *Graph) findByName(name string) (string, bool) {
	name = strings.TrimSuffix(path.Base(name), ".md")
	for p := range g.byPath {
		if strings.TrimSuffix(path.Base(p), ".md") == name {
			return p, true
		}
	}
	return "", false
}

// codeTargetExists checks a code reference against the anchored checkout for the
// document's project. Without an anchor the reference cannot be judged, so it is
// treated as intact rather than reported as broken — accusing a user of a dead
// link because contextd was never told where their code lives would be worse
// than staying quiet.
func (g *Graph) codeTargetExists(from, target string) bool {
	project := projectOf(from)
	if project == "" {
		return true
	}
	base, ok := g.Anchors[project]
	if !ok || base == "" {
		return true
	}
	clean := strings.TrimPrefix(path.Clean(target), "./")
	_, err := os.Stat(filepath.Join(base, filepath.FromSlash(clean)))
	return err == nil
}

// finalize computes degrees, backlinks, orphans and rank.
func (g *Graph) finalize() {
	inbound := map[string]int{}
	for _, e := range g.Edges {
		if e.Broken {
			g.Broken = append(g.Broken, e)
			continue
		}
		if i, ok := g.byPath[e.From]; ok {
			g.Nodes[i].OutDegree++
		}
		if e.Code {
			continue // code targets are not nodes in the space
		}
		inbound[e.To]++
	}
	for p, n := range inbound {
		if i, ok := g.byPath[p]; ok {
			g.Nodes[i].InDegree = n
		}
	}

	g.rank()

	for _, n := range g.Nodes {
		if n.InDegree == 0 && n.Path != "context-entry.md" {
			g.Orphans = append(g.Orphans, n.Path)
		}
	}
	sort.Strings(g.Orphans)
	sort.Slice(g.Nodes, func(i, j int) bool {
		if g.Nodes[i].Rank != g.Nodes[j].Rank {
			return g.Nodes[i].Rank > g.Nodes[j].Rank
		}
		return g.Nodes[i].Path < g.Nodes[j].Path
	})
	// Sorting invalidated the index; rebuild so lookups keep working.
	g.byPath = make(map[string]int, len(g.Nodes))
	for i, n := range g.Nodes {
		g.byPath[n.Path] = i
	}
}

// rank runs PageRank over the space's links, then adjusts by what the author
// declared: a document marked important should outrank one that merely happens
// to be linked a lot, and a stale document should sink whatever links to it.
func (g *Graph) rank() {
	n := len(g.Nodes)
	if n == 0 {
		return
	}
	idx := map[string]int{}
	for i, node := range g.Nodes {
		idx[node.Path] = i
	}

	out := make([][]int, n)
	for _, e := range g.Edges {
		if e.Broken || e.Code {
			continue
		}
		from, okF := idx[e.From]
		to, okT := idx[e.To]
		if !okF || !okT || from == to {
			continue
		}
		out[from] = append(out[from], to)
	}

	const (
		damping = 0.85
		rounds  = 30
	)
	rank := make([]float64, n)
	next := make([]float64, n)
	for i := range rank {
		rank[i] = 1 / float64(n)
	}
	for r := 0; r < rounds; r++ {
		leaked := 0.0
		for i := range next {
			next[i] = (1 - damping) / float64(n)
		}
		for i, targets := range out {
			if len(targets) == 0 {
				leaked += rank[i] // dangling nodes spread their weight evenly
				continue
			}
			share := damping * rank[i] / float64(len(targets))
			for _, t := range targets {
				next[t] += share
			}
		}
		spread := damping * leaked / float64(n)
		for i := range next {
			next[i] += spread
		}
		rank, next = next, rank
	}

	for i := range g.Nodes {
		r := rank[i]
		switch strings.ToLower(g.Nodes[i].Importance) {
		case "high", "critical":
			r *= 1.6
		case "low":
			r *= 0.6
		}
		if g.Nodes[i].Stale {
			r *= 0.5
		}
		g.Nodes[i].Rank = r
	}
}

// Node returns a node by path.
func (g *Graph) Node(p string) (Node, bool) {
	i, ok := g.byPath[p]
	if !ok {
		return Node{}, false
	}
	return g.Nodes[i], true
}

// Neighbours returns what a document points at and what points back at it —
// the two questions navigation is made of.
func (g *Graph) Neighbours(p string) (outbound, inbound []Edge) {
	for _, e := range g.Edges {
		switch {
		case e.From == p:
			outbound = append(outbound, e)
		case e.To == p && !e.Code:
			inbound = append(inbound, e)
		}
	}
	return outbound, inbound
}

func skipDir(name string) bool {
	switch name {
	case ".contextverse", ".sync", ".git", "node_modules", ".venv", "__pycache__":
		return true
	}
	return false
}

func skipFile(rel string) bool {
	if strings.HasPrefix(rel, ".") {
		return true
	}
	switch rel {
	case "config.yaml", "template.yaml", "TEMPLATE.md":
		return true
	}
	return false
}

func projectOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) >= 2 && parts[0] == "projects" {
		return parts[1]
	}
	return ""
}

func titleFromPath(rel string) string {
	return strings.TrimSuffix(path.Base(rel), path.Ext(rel))
}

func titleFromContent(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func importanceFromFrontmatter(raw []byte) string {
	s := string(raw)
	if !strings.HasPrefix(s, "---") {
		return ""
	}
	end := strings.Index(s[3:], "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(s[3:3+end], "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "importance" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
