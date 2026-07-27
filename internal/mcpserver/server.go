package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/graph"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/search"
	"github.com/abyssmemes/contextverse/internal/space"
	"github.com/abyssmemes/contextverse/internal/version"
)

// Options configure the local-space MCP server.
type Options struct {
	SpaceRoot string
}

// Run starts the MCP server on stdio until the client disconnects.
func Run(ctx context.Context, opts Options) error {
	if opts.SpaceRoot == "" {
		return fmt.Errorf("space root is required")
	}
	logx.L().Info("mcp serve starting", "space_root", opts.SpaceRoot, "version", version.Version)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "contextverse",
		Title:   "ContextVerse",
		Version: version.Version,
	}, nil)

	h := &handlers{root: opts.SpaceRoot}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_status",
		Description: "Summarize the local ContextVerse space (existence, missing files, projects, identity).",
	}, h.status)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_list",
		Description: "List markdown/context files under the space (relative paths).",
	}, h.list)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_get",
		Description: "Read one file from the context space by relative path (e.g. team/principles.md).",
	}, h.get)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_search",
		Description: "Search file names and contents in the space for a query string (case-insensitive).",
	}, h.search)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_neighbors",
		Description: "Show what a document links to and what links back to it. Use this to move around the space instead of reading every file.",
	}, h.neighbors)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "context_map",
		Description: "The map of the space: documents ranked by how often they are referenced. Start here, then fetch only what you need.",
	}, h.contextMap)

	server.AddResource(&mcp.Resource{
		URI:         "contextverse://local/context-entry.md",
		Name:        "context-entry",
		Description: "Universal entry point for the local space",
		MIMEType:    "text/markdown",
	}, h.readResource)

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "contextverse://local/{+path}",
		Name:        "space-file",
		Description: "Any file in the local ContextVerse space",
		MIMEType:    "text/markdown",
	}, h.readResource)

	return server.Run(ctx, &mcp.StdioTransport{})
}

type handlers struct {
	root string
}

type emptyIn struct{}

type getIn struct {
	Path string `json:"path" jsonschema:"relative path within the space, e.g. identity/me.md"`
}

type neighborsIn struct {
	Path string `json:"path" jsonschema:"relative path within the space, e.g. team/principles.md"`
}

type mapIn struct {
	Budget int `json:"budget,omitempty" jsonschema:"approximate token budget for the map (default 700)"`
}

type searchIn struct {
	Query string `json:"query" jsonschema:"substring to search for in paths and file contents"`
	Limit int    `json:"limit,omitempty" jsonschema:"max matches to return (default 20)"`
}

type listIn struct {
	Prefix string `json:"prefix,omitempty" jsonschema:"optional path prefix to filter, e.g. team/"`
}

func (h *handlers) status(ctx context.Context, req *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	st, err := space.Inspect(h.root)
	if err != nil {
		return toolErr(err), nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "space_root: %s\n", st.SpaceRoot)
	fmt.Fprintf(&b, "exists: %v\n", st.Exists)
	if st.IdentityName != "" {
		fmt.Fprintf(&b, "identity: %s\n", st.IdentityName)
	}
	if len(st.Missing) > 0 {
		fmt.Fprintf(&b, "missing: %s\n", strings.Join(st.Missing, ", "))
	} else {
		fmt.Fprintf(&b, "missing: (none)\n")
	}
	if len(st.Projects) == 0 {
		fmt.Fprintf(&b, "projects: (none)\n")
	} else {
		fmt.Fprintf(&b, "projects: %s\n", strings.Join(st.Projects, ", "))
	}
	return textResult(b.String()), nil, nil
}

func (h *handlers) list(ctx context.Context, req *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, any, error) {
	files, err := listFiles(h.root, in.Prefix)
	if err != nil {
		return toolErr(err), nil, nil
	}
	if len(files) == 0 {
		return textResult("(no files)"), nil, nil
	}
	return textResult(strings.Join(files, "\n")), nil, nil
}

func (h *handlers) get(ctx context.Context, req *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, any, error) {
	if in.Path == "" {
		return toolErr(fmt.Errorf("path is required")), nil, nil
	}
	content, err := readSpaceFile(h.root, in.Path)
	if err != nil {
		return toolErr(err), nil, nil
	}
	return textResult(content), nil, nil
}

func (h *handlers) search(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
	// Shared with `contextd search`, deliberately: the AI and the person whose
	// space it is must get the same answers to "what is in here". Two search
	// implementations is how those quietly diverge.
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	res, err := search.Search(search.Options{Root: h.root, Query: in.Query, Limit: limit})
	if err != nil {
		return toolErr(err), nil, nil
	}
	if len(res.Matches) == 0 {
		return textResult("(no matches)"), nil, nil
	}
	lines := make([]string, 0, len(res.Matches))
	for _, m := range res.Matches {
		if m.Name {
			lines = append(lines, m.Path+" (filename)")
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d: %s", m.Path, m.Line, m.Text))
	}
	return textResult(strings.Join(lines, "\n")), nil, nil
}

// neighbors and contextMap give the model a way to navigate rather than
// consume: ask what a document connects to, or ask for the map, instead of
// being handed every file at session start.
func (h *handlers) neighbors(ctx context.Context, req *mcp.CallToolRequest, in neighborsIn) (*mcp.CallToolResult, any, error) {
	g, err := h.graph()
	if err != nil {
		return toolErr(err), nil, nil
	}
	node, ok := g.Node(in.Path)
	if !ok {
		return toolErr(fmt.Errorf("%s is not in the space", in.Path)), nil, nil
	}
	out, back := g.Neighbours(in.Path)

	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n\n", node.Path, node.Title)
	if node.Stale {
		fmt.Fprintf(&b, "WARNING: past its review date; treat with suspicion.\n\n")
	}
	fmt.Fprintf(&b, "Points at:\n")
	if len(out) == 0 {
		fmt.Fprintf(&b, "  (nothing)\n")
	}
	for _, e := range out {
		mark := ""
		if e.Broken {
			mark = "  [MISSING]"
		} else if e.Code {
			mark = "  [code]"
		}
		fmt.Fprintf(&b, "  %s%s\n", e.To, mark)
	}
	fmt.Fprintf(&b, "\nPointed at by:\n")
	if len(back) == 0 {
		fmt.Fprintf(&b, "  (nothing)\n")
	}
	for _, e := range back {
		fmt.Fprintf(&b, "  %s\n", e.From)
	}
	return textResult(b.String()), nil, nil
}

func (h *handlers) contextMap(ctx context.Context, req *mcp.CallToolRequest, in mapIn) (*mcp.CallToolResult, any, error) {
	g, err := h.graph()
	if err != nil {
		return toolErr(err), nil, nil
	}
	return textResult(graph.RenderMap(g, graph.MapOptions{Budget: in.Budget, SpaceRoot: h.root})), nil, nil
}

// graph derives the space map, resolving code references against the project
// checkouts activate recorded.
func (h *handlers) graph() (*graph.Graph, error) {
	anchors := map[string]string{}
	if cfg, err := config.Load(h.root); err == nil {
		for _, a := range cfg.Anchors {
			anchors[a.Project] = a.Path
		}
	}
	return graph.Load(graph.Options{Root: h.root, Anchors: anchors})
}

func (h *handlers) readResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	rel := strings.TrimPrefix(uri, "contextverse://local/")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return nil, fmt.Errorf("empty resource path")
	}
	content, err := readSpaceFile(h.root, rel)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     content,
		}},
	}, nil
}

func listFiles(root, prefix string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "config.yaml" || rel == "template.yaml" {
			return nil
		}
		if prefix != "" && !strings.HasPrefix(rel, strings.TrimPrefix(prefix, "/")) {
			return nil
		}
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".md", ".yaml", ".yml", ".txt", "":
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func readSpaceFile(root, rel string) (string, error) {
	rel = filepath.Clean(rel)
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid path %q", rel)
	}
	full := filepath.Join(root, rel)
	// ensure still under root
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absFull, absRoot+string(os.PathSeparator)) && absFull != absRoot {
		return "", fmt.Errorf("path escapes space root")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: s},
		},
	}
}

func toolErr(err error) *mcp.CallToolResult {
	logx.L().Error("mcp tool error", "err", err)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "error: " + err.Error()},
		},
		IsError: true,
	}
}
