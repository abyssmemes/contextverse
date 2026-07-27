package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/graph"
)

// loadGraph derives the space's graph, passing in the project anchors so code
// references can be checked. Never asks the user to build anything: a map with
// a build step is a map that goes stale, which is the failure this replaces.
func loadGraph() (*graph.Graph, error) {
	root, err := resolveSpaceRoot()
	if err != nil {
		return nil, err
	}
	anchors := map[string]string{}
	if cfg, err := config.Load(root); err == nil {
		for _, a := range cfg.Anchors {
			anchors[a.Project] = a.Path
		}
	}
	return graph.Load(graph.Options{Root: root, Anchors: anchors})
}

func newGraphCmd() *cobra.Command {
	var (
		format  string
		limit   int
		orphans bool
		broken  bool
	)
	cmd := &cobra.Command{
		Use:   "graph [path]",
		Short: "The map of your space: what links to what",
		Long: `Show the space as a graph, derived from the links your documents contain.

Nothing is built and nothing is indexed by a model: every edge comes from text
you wrote — a [[wikilink]], a Markdown link, or a path like ./scripts/deploy.sh
— and points at a line you can go and look at. The graph is recomputed when the
space changes and read from cache when it has not.

With a path, shows that document's neighbourhood: what it points at, and what
points back.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := loadGraph()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return showNeighbourhood(cmd, g, args[0])
			}
			switch {
			case orphans:
				return emit(cmd.OutOrStdout(), g.Orphans, func(w io.Writer) error {
					if len(g.Orphans) == 0 {
						fmt.Fprintln(w, "no orphans — every document is reachable")
						return nil
					}
					fmt.Fprintf(w, "%d document(s) nothing links to:\n\n", len(g.Orphans))
					for _, o := range g.Orphans {
						fmt.Fprintf(w, "  %s\n", o)
					}
					return nil
				})
			case broken:
				return emit(cmd.OutOrStdout(), g.Broken, func(w io.Writer) error {
					if len(g.Broken) == 0 {
						fmt.Fprintln(w, "no broken links")
						return nil
					}
					fmt.Fprintf(w, "%d broken link(s):\n\n", len(g.Broken))
					for _, e := range g.Broken {
						kind := ""
						if e.Code {
							kind = " (code)"
						}
						fmt.Fprintf(w, "  %s:%d → %s%s\n", e.From, e.Line, e.Raw, kind)
					}
					return nil
				})
			}

			switch format {
			case "mermaid":
				_, err := io.WriteString(cmd.OutOrStdout(), mermaid(g, limit))
				return err
			case "", "text":
				return emit(cmd.OutOrStdout(), g, func(w io.Writer) error {
					return writeGraphText(w, g, limit)
				})
			default:
				return fmt.Errorf("unknown --format %q (text, mermaid)", format)
			}
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "text or mermaid")
	cmd.Flags().IntVar(&limit, "limit", 25, "how many documents to show, highest ranked first")
	cmd.Flags().BoolVar(&orphans, "orphans", false, "list documents nothing links to")
	cmd.Flags().BoolVar(&broken, "broken", false, "list links that point at nothing")
	return cmd
}

func writeGraphText(w io.Writer, g *graph.Graph, limit int) error {
	fmt.Fprintf(w, "%s\n\n", g.Summary())
	shown := g.Nodes
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, n := range shown {
		marks := ""
		if n.Stale {
			marks += " ⚠stale"
		}
		title := n.Title
		if title == "" {
			title = n.Path
		}
		fmt.Fprintf(w, "  %-42s %2d in  %2d out  %s%s\n", n.Path, n.InDegree, n.OutDegree, title, marks)
	}
	if len(g.Nodes) > len(shown) {
		fmt.Fprintf(w, "\n  … %d more (--limit 0 for all)\n", len(g.Nodes)-len(shown))
	}
	if len(g.Broken) > 0 {
		fmt.Fprintf(w, "\n%d broken link(s) — contextd graph --broken\n", len(g.Broken))
	}
	if len(g.Orphans) > 0 {
		fmt.Fprintf(w, "%d orphan(s) — contextd graph --orphans\n", len(g.Orphans))
	}
	return nil
}

func showNeighbourhood(cmd *cobra.Command, g *graph.Graph, path string) error {
	node, ok := g.Node(path)
	if !ok {
		return fmt.Errorf("%s is not in the space", path)
	}
	out, in := g.Neighbours(path)

	type view struct {
		Path     string       `json:"path" yaml:"path"`
		Title    string       `json:"title,omitempty" yaml:"title,omitempty"`
		Stale    bool         `json:"stale,omitempty" yaml:"stale,omitempty"`
		Outbound []graph.Edge `json:"outbound" yaml:"outbound"`
		Inbound  []graph.Edge `json:"inbound" yaml:"inbound"`
	}
	v := view{Path: node.Path, Title: node.Title, Stale: node.Stale, Outbound: out, Inbound: in}

	return emit(cmd.OutOrStdout(), v, func(w io.Writer) error {
		fmt.Fprintf(w, "%s", node.Path)
		if node.Title != "" && node.Title != node.Path {
			fmt.Fprintf(w, "  — %s", node.Title)
		}
		if node.Stale {
			fmt.Fprint(w, "  ⚠ stale")
		}
		fmt.Fprint(w, "\n\n")

		fmt.Fprintf(w, "Points at (%d):\n", len(out))
		if len(out) == 0 {
			fmt.Fprintln(w, "  —")
		}
		for _, e := range out {
			mark := ""
			if e.Broken {
				mark = "  ✗ missing"
			} else if e.Code {
				mark = "  (code)"
			}
			fmt.Fprintf(w, "  :%-4d %s%s\n", e.Line, e.To, mark)
		}

		fmt.Fprintf(w, "\nPointed at by (%d):\n", len(in))
		if len(in) == 0 {
			fmt.Fprintln(w, "  — nothing links here")
		}
		for _, e := range in {
			fmt.Fprintf(w, "  %s:%d\n", e.From, e.Line)
		}
		return nil
	})
}

// mermaid renders the top-ranked slice of the graph. Whole graphs of any size
// render as an unreadable hairball, so this is deliberately a summary.
func mermaid(g *graph.Graph, limit int) string {
	if limit <= 0 || limit > len(g.Nodes) {
		limit = len(g.Nodes)
	}
	keep := map[string]string{}
	for i, n := range g.Nodes[:limit] {
		keep[n.Path] = fmt.Sprintf("n%d", i)
	}

	var b strings.Builder
	b.WriteString("graph LR\n")

	paths := make([]string, 0, len(keep))
	for p := range keep {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		node, _ := g.Node(p)
		label := node.Title
		if label == "" {
			label = p
		}
		label = strings.ReplaceAll(label, `"`, "'")
		b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", keep[p], label))
	}
	for _, e := range g.Edges {
		if e.Broken || e.Code {
			continue
		}
		from, okF := keep[e.From]
		to, okT := keep[e.To]
		if !okF || !okT || from == to {
			continue
		}
		b.WriteString(fmt.Sprintf("  %s --> %s\n", from, to))
	}
	return b.String()
}
