package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/orkcom-tech/contextverse/internal/config"
	"github.com/orkcom-tech/contextverse/internal/graph"
)

// The Graph tab turns the space from a list of files into something you move
// through: pick a document, press enter, and you are looking at what it points
// at and what points back — then enter again to walk to one of those.

func loadGraphCmd(spaceRoot string) tea.Cmd {
	return func() tea.Msg {
		anchors := map[string]string{}
		if cfg, err := config.Load(spaceRoot); err == nil {
			for _, a := range cfg.Anchors {
				anchors[a.Project] = a.Path
			}
		}
		g, err := graph.Load(graph.Options{Root: spaceRoot, Anchors: anchors})
		return graphLoadedMsg{g: g, err: err}
	}
}

// graphRow is one selectable line, carrying the path it would navigate to so
// the cursor and the target cannot drift apart.
type graphRow struct {
	label  string
	target string
}

func (m model) graphRows() []graphRow {
	if m.graph == nil {
		return nil
	}
	if m.graphFocus == "" {
		rows := make([]graphRow, 0, len(m.graph.Nodes))
		for _, n := range m.graph.Nodes {
			marks := ""
			if n.Stale {
				marks = " ⚠"
			}
			rows = append(rows, graphRow{
				label:  fmt.Sprintf("%-38s %2d in %2d out%s", truncatePath(n.Path, 38), n.InDegree, n.OutDegree, marks),
				target: n.Path,
			})
		}
		return rows
	}

	out, in := m.graph.Neighbours(m.graphFocus)
	rows := make([]graphRow, 0, len(out)+len(in))
	for _, e := range out {
		mark := "→"
		target := e.To
		switch {
		case e.Broken:
			mark = "✗"
			target = "" // a missing target is not somewhere to walk to
		case e.Code:
			mark = "⟶code"
			target = ""
		}
		rows = append(rows, graphRow{
			label:  fmt.Sprintf("%s %-34s :%d", mark, truncatePath(e.To, 34), e.Line),
			target: target,
		})
	}
	for _, e := range in {
		rows = append(rows, graphRow{
			label:  fmt.Sprintf("← %-34s :%d", truncatePath(e.From, 34), e.Line),
			target: e.From,
		})
	}
	return rows
}

func (m model) graphSelection() string {
	rows := m.graphRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return ""
	}
	return rows[m.cursor].target
}

func (m model) renderGraph(w, h int) string {
	if m.graphErr != "" {
		body := styleErr.Render(m.graphErr)
		return stylePane.Width(w - 2).Height(h).Render(fillHeight(body, h-2))
	}
	if m.graph == nil {
		return stylePane.Width(w - 2).Height(h).Render(fillHeight(styleMuted.Render("deriving the graph…"), h-2))
	}

	rows := m.graphRows()
	labels := make([]string, 0, len(rows))
	for _, r := range rows {
		labels = append(labels, r.label)
	}

	title := "Graph"
	empty := "(no documents)"
	if m.graphFocus != "" {
		title = "Around " + m.graphFocus
		empty = "(nothing connects here)"
	}
	left := renderSelectableList(labels, m.cursor, w*58/100-4, h-4, empty)

	return SplitTwo(title, left, "Detail", m.graphDetail(), w, h, 58)
}

func (m model) graphDetail() string {
	var b strings.Builder
	b.WriteString(m.graph.Summary())
	b.WriteString("\n\n")

	if m.graphFocus != "" {
		if n, ok := m.graph.Node(m.graphFocus); ok {
			fmt.Fprintf(&b, "%s\n", n.Path)
			if n.Title != "" {
				fmt.Fprintf(&b, "%s\n", n.Title)
			}
			if n.Stale {
				b.WriteString(styleErr.Render("past its review date") + "\n")
			}
			fmt.Fprintf(&b, "\n%d in · %d out\n", n.InDegree, n.OutDegree)
		}
		b.WriteString("\nenter  walk to the selected document\nesc    back to the whole graph\n")
		return b.String()
	}

	if sel := m.graphSelection(); sel != "" {
		if n, ok := m.graph.Node(sel); ok {
			fmt.Fprintf(&b, "%s\n", n.Path)
			if n.Title != "" && n.Title != n.Path {
				fmt.Fprintf(&b, "%s\n", n.Title)
			}
			if n.Owner != "" {
				fmt.Fprintf(&b, "owner %s\n", n.Owner)
			}
			if n.Stale {
				b.WriteString(styleErr.Render("past its review date") + "\n")
			}
			fmt.Fprintf(&b, "\n%d references in · %d out\n", n.InDegree, n.OutDegree)
		}
	}

	b.WriteString("\nenter  open its connections\nr      re-derive\n")
	if len(m.graph.Broken) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleErr.Render(fmt.Sprintf("%d broken link(s)", len(m.graph.Broken))))
		for i, e := range m.graph.Broken {
			if i >= 3 {
				fmt.Fprintf(&b, "  … %d more\n", len(m.graph.Broken)-i)
				break
			}
			fmt.Fprintf(&b, "  %s:%d → %s\n", truncatePath(e.From, 22), e.Line, truncatePath(e.Raw, 22))
		}
	}
	if len(m.graph.Orphans) > 0 {
		fmt.Fprintf(&b, "\n%d document(s) nothing links to\n", len(m.graph.Orphans))
	}
	return b.String()
}

// truncatePath keeps the informative end of a path when it will not fit, since
// the filename says more than the directory it lives in.
func truncatePath(p string, max int) string {
	if len(p) <= max || max < 4 {
		return p
	}
	return "…" + p[len(p)-(max-1):]
}
