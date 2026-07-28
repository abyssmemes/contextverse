package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/orkcom-tech/contextverse/internal/graph"
	"github.com/orkcom-tech/contextverse/internal/logx"
	"github.com/orkcom-tech/contextverse/internal/plugins"
	templatepkg "github.com/orkcom-tech/contextverse/internal/template"
)

func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Session-start context delivery (inject, list formats)",
	}
	cmd.AddCommand(newContextInjectCmd())
	return cmd
}

func newContextInjectCmd() *cobra.Command {
	var (
		format  string
		list    bool
		project string
		mode    string
		budget  int
	)
	cmd := &cobra.Command{
		Use:   "inject",
		Short: "Emit session-start context payload to stdout (for AI hooks)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if list {
				for _, f := range plugins.InjectFormats() {
					fmt.Fprintln(cmd.OutOrStdout(), f)
				}
				return nil
			}
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			var out string
			switch mode {
			case "map":
				out, err = injectMap(root, cwd, project, format, budget)
			case "", "entry-set":
				out, err = plugins.Inject(format, root, cwd, project)
			default:
				return fmt.Errorf("unknown --mode %q (entry-set, map)", mode)
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "claude-hook", "output format (claude-hook|text)")
	cmd.Flags().BoolVar(&list, "list", false, "list known inject formats")
	cmd.Flags().StringVar(&project, "project", "", "active project under projects/ (default: infer from cwd)")
	cmd.Flags().StringVar(&mode, "mode", "entry-set", "entry-set (send the documents) or map (send the graph and let the model fetch)")
	cmd.Flags().IntVar(&budget, "budget", 700, "approximate token budget for --mode map")
	return cmd
}

// injectMap emits the graph map instead of the entry set.
//
// This is the other arm of the eager-versus-lazy question, and it is opt-in on
// purpose. Whether a model does better handed the whole entry set or handed a
// map plus the means to fetch has never been measured, and switching the
// default on an unmeasured belief is precisely the mistake the benchmark exists
// to prevent.
func injectMap(root, cwd, project, format string, budget int) (string, error) {
	if project == "" {
		project = plugins.ResolveProject(root, cwd)
	}
	g, err := loadGraph()
	if err != nil {
		return "", err
	}
	body := graph.RenderMap(g, graph.MapOptions{Project: project, Budget: budget, SpaceRoot: root})

	switch strings.TrimSpace(strings.ToLower(format)) {
	case "claude-hook", "claude":
		payload := map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "SessionStart",
				"additionalContext": body,
			},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(raw) + "\n", nil
	default:
		return body, nil
	}
}

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Wire AI client session-start slots (client-integration templates)",
	}
	cmd.AddCommand(newPluginListCmd())
	cmd.AddCommand(newPluginInstallCmd())
	cmd.AddCommand(newPluginRefreshCmd())
	return cmd
}

func newPluginRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Re-fetch community client-integration templates from the catalog",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := templatepkg.SyncClientIntegrations("", "", true, nil)
			if err != nil {
				return err
			}
			cat, err := plugins.LoadCatalog(dir)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "refreshed %d community integration(s) → %s\n", len(cat), dir)
			for _, in := range cat {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\t%s\n", in.ID, in.Display)
			}
			return nil
		},
	}
}

// PluginEntry is one client integration, as `plugin list` reports it. Detected
// is the field worth scripting over: it answers "is this machine set up for
// Cursor" without parsing a marker out of a table.
type PluginEntry struct {
	ID        string `json:"id" yaml:"id"`
	Mechanism string `json:"mechanism" yaml:"mechanism"`
	Display   string `json:"display" yaml:"display"`
	Detected  bool   `json:"detected" yaml:"detected"`
	How       string `json:"how,omitempty" yaml:"how,omitempty"`
}

func newPluginListCmd() *cobra.Command {
	var refresh, offline bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List known client-integration templates (embedded + community)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cat, err := plugins.LoadDefaultCatalog(plugins.CatalogOpts{Refresh: refresh, Offline: offline})
			if err != nil {
				return err
			}
			vars, err := plugins.DefaultVars("", "", "")
			if err != nil {
				return err
			}
			detected := map[string]string{}
			for _, d := range plugins.Detect(cat, vars) {
				detected[d.Integration.ID] = d.How
			}
			out := make([]PluginEntry, 0, len(cat))
			for _, in := range cat {
				how, ok := detected[in.ID]
				out = append(out, PluginEntry{
					ID:        in.ID,
					Mechanism: in.Mechanism,
					Display:   in.Display,
					Detected:  ok,
					How:       how,
				})
			}
			return emit(cmd.OutOrStdout(), out, func(w io.Writer) error {
				tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
				for _, e := range out {
					mark := ""
					if e.Detected {
						mark = "\tdetected(" + e.How + ")"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s%s\n", e.ID, e.Mechanism, e.Display, mark)
				}
				return tw.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-fetch community catalog before listing")
	cmd.Flags().BoolVar(&offline, "offline", false, "embedded + local only (no network)")
	return cmd
}

func newPluginInstallCmd() *cobra.Command {
	var (
		project        string
		nonInteractive bool
		refresh        bool
		offline        bool
	)
	cmd := &cobra.Command{
		Use:   "install [client-id...]",
		Short: "Apply client-integration templates (default: all detected)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				root = ""
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if project == "" {
				project = plugins.ResolveProject(root, cwd)
			}
			vars, err := plugins.DefaultVars(root, cwd, project)
			if err != nil {
				return err
			}
			cat, err := plugins.LoadDefaultCatalog(plugins.CatalogOpts{Refresh: refresh, Offline: offline})
			if err != nil {
				return err
			}
			if len(args) == 0 {
				interactive := !nonInteractive
				if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
					interactive = false
				}
				results, err := plugins.ApplyDetected(cat, vars, plugins.ApplyOpts{Interactive: interactive, Chooser: pluginChooser})
				if err != nil {
					return err
				}
				for _, r := range results {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", r.ID, r.Action, r.Target)
				}
				return nil
			}
			for _, id := range args {
				res, err := plugins.ApplyByID(cat, id, vars)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", res.ID, res.Action, res.Target)
				logx.L().Info("plugin install", "id", id, "action", res.Action)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "active project name")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "never prompt; print manual instructions if nothing detected")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-fetch community catalog before install")
	cmd.Flags().BoolVar(&offline, "offline", false, "embedded + local only (no network)")
	return cmd
}

func applySessionStartPlugins(spaceRoot, cwd, project string, silent bool) error {
	if project == "" {
		project = plugins.ResolveProject(spaceRoot, cwd)
	}
	vars, err := plugins.DefaultVars(spaceRoot, cwd, project)
	if err != nil {
		return err
	}
	cat, err := plugins.LoadDefaultCatalog(plugins.CatalogOpts{})
	if err != nil {
		return err
	}
	interactive := !silent
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		interactive = false
	}
	results, err := plugins.ApplyDetected(cat, vars, plugins.ApplyOpts{Interactive: interactive, Chooser: pluginChooser})
	if err != nil {
		return err
	}
	if silent {
		return nil
	}
	for _, r := range results {
		fmt.Fprintf(os.Stdout, "  ✅ plugin %s (%s) → %s\n", r.ID, r.Action, r.Target)
	}
	return nil
}
