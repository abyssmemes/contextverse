package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/entrypoint"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/mcpserver"
	"github.com/abyssmemes/contextverse/internal/plugins"
	"github.com/abyssmemes/contextverse/internal/space"
	templatepkg "github.com/abyssmemes/contextverse/internal/template"
	"github.com/abyssmemes/contextverse/internal/version"
)

var (
	flagDebug     bool
	flagSpaceRoot string
)

// Help groups. The command surface is wide enough that one alphabetical list
// tells a reader nothing about where to start, so the root help is split by the
// job being done rather than by the noun being acted on.
const (
	groupSetup     = "setup"
	groupSpace     = "space"
	groupSync      = "sync"
	groupAI        = "ai"
	groupServer    = "server"
	groupInterface = "interface"
)

// Execute runs the root command.
func Execute() error {
	root := newRoot()
	return root.Execute()
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "contextd",
		Short:         "Portable, vendor-neutral context for AI",
		Long:          "contextd manages a ContextVerse space and generates entry points so any AI tool can read the same curated context.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			logx.SetDebug(flagDebug)
		},
	}
	root.PersistentFlags().BoolVar(&flagDebug, "debug", false, "enable debug logging")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "structured JSON output (where supported)")
	root.PersistentFlags().BoolVar(&flagYAML, "yaml", false, "structured YAML output (where supported)")
	root.PersistentFlags().StringVar(&flagSpaceRoot, "dir", "", "context space root (default: ~/.context)")
	root.PersistentFlags().StringVar(&flagServerDir, "server-dir", "", "server data directory (default: ~/.contextverse-server)")

	root.AddGroup(
		&cobra.Group{ID: groupSetup, Title: "Set up and inspect:"},
		&cobra.Group{ID: groupSpace, Title: "Work on your context space:"},
		&cobra.Group{ID: groupSync, Title: "Sync and storage:"},
		&cobra.Group{ID: groupAI, Title: "Deliver context to AI tools:"},
		&cobra.Group{ID: groupServer, Title: "Administer a server:"},
		&cobra.Group{ID: groupInterface, Title: "Interfaces and help:"},
	)

	addGrouped(root, groupSetup,
		newInitCmd(), newActivateCmd(), newStatusCmd(), newVersionCmd())
	addGrouped(root, groupSpace,
		newSpaceCmd(), newFileCmd(), newSearchCmd(), newGraphCmd(), newHistoryCmd(), newIndexCmd(), newTemplateCmd(), newFreshnessCmd())
	addGrouped(root, groupSync,
		newPullCmd(), newPushCmd(), newDaemonCmd(), newBackendCmd())
	addGrouped(root, groupAI,
		newMCPCmd(), newPluginCmd(), newContextCmd(), newExportCmd())
	addGrouped(root, groupServer,
		newServerCmd(), newUserCmd(), newAuthCmd(), newPolicyCmd(), newACLCmd(), newAuditCmd(), newWebhooksCmd())
	addGrouped(root, groupInterface,
		newTUICmd(), newUICmd(), newCompletionCmd())
	root.SetHelpCommandGroupID(groupInterface)

	return root
}

// addGrouped files each command under a root help group. Assigning the group at
// registration keeps the grouping readable in one place instead of scattering
// GroupID across every constructor.
func addGrouped(root *cobra.Command, group string, cmds ...*cobra.Command) {
	for _, c := range cmds {
		c.GroupID = group
		root.AddCommand(c)
	}
}

func resolveSpaceRoot() (string, error) {
	if flagSpaceRoot != "" {
		return flagSpaceRoot, nil
	}
	return config.DefaultSpaceRoot()
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print contextd version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "contextd %s\n", version.Version)
		},
	}
}

func newInitCmd() *cobra.Command {
	var reconfigure bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a ContextVerse installation",
		Long: `Create and configure a context space.

Run bare for a guided setup that picks the mode with you and explains each
choice. Use --reconfigure to change an existing installation. The subcommands
stay available for scripts and CI:
  solo     local-only space on this machine, no server and no account
  client   sync an existing space from a server you have a token for
  server   host a space for a team (starts the setup UI; --noui for headless)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if reconfigure {
				return runReconfigure(cmd)
			}
			return runInitWizard(cmd)
		},
	}
	cmd.Flags().BoolVar(&reconfigure, "reconfigure", false, "change settings of an existing installation")
	cmd.AddCommand(newInitSoloCmd())
	cmd.AddCommand(newInitServerCmd())
	cmd.AddCommand(newInitClientCmd())
	return cmd
}

func newInitSoloCmd() *cobra.Command {
	var (
		name           string
		role           string
		language       string
		tools          string
		templateName   string
		templatePath   string
		nonInteractive bool
		force          bool
		refreshTpl     bool
	)

	cmd := &cobra.Command{
		Use:   "solo",
		Short: "Create & configure a local-only context space",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			logx.L().Info("init solo starting", "space_root", root)

			if !nonInteractive {
				in := bufio.NewReader(cmd.InOrStdin())
				name = askLine(in, "Your name", name)
				role = askLine(in, "Your role", role)
				language = askLine(in, "Preferred language", orDefault(language, "English"))
				tools = askLine(in, "Tools you use", tools)
			} else {
				if name == "" {
					return fmt.Errorf("--name is required with --non-interactive")
				}
				if language == "" {
					language = "English"
				}
			}

			if config.Exists(root) && !force {
				return fmt.Errorf("already initialized at %s (use --force to recreate template files; config will be rewritten)", root)
			}

			// Same builder the wizard uses, so the two entry points cannot drift.
			if err := createSoloSpace(cmd, root, soloSetup{
				Name:            name,
				Role:            role,
				Language:        language,
				Tools:           tools,
				Template:        templateName,
				TemplatePath:    templatePath,
				RefreshTemplate: refreshTpl,
				Force:           force,
				Quiet:           true,
			}); err != nil {
				return err
			}
			logx.L().Info("solo init complete", "space_root", root)

			fmt.Fprintf(cmd.OutOrStdout(), "\n✅ Solo context space initialized at %s\n\n", root)
			fmt.Fprintf(cmd.OutOrStdout(), "No sync configured. All data stays on this machine.\n")
			fmt.Fprintf(cmd.OutOrStdout(), "Next:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  1. Edit %s/identity/me.md if needed\n", root)
			fmt.Fprintf(cmd.OutOrStdout(), "  2. cd <your-project> && contextd activate\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "your name")
	cmd.Flags().StringVar(&role, "role", "", "your role")
	cmd.Flags().StringVar(&language, "language", "", "preferred language")
	cmd.Flags().StringVar(&tools, "tools", "", "tools you use")
	cmd.Flags().StringVar(&templateName, "template", "solo-default", "template name from contextverse-templates catalog")
	cmd.Flags().StringVar(&templatePath, "template-path", "", "path to a local template directory (overrides --template)")
	cmd.Flags().BoolVar(&refreshTpl, "refresh-template", false, "re-fetch catalog template (ignore cache)")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "do not prompt; require flags")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing space")
	return cmd
}

func newActivateCmd() *cobra.Command {
	var (
		project     string
		silent      bool
		offline     bool
		pullTimeout time.Duration
		requireSync bool
	)
	cmd := &cobra.Command{
		Use:   "activate",
		Short: "Generate AI entry points and wire session-start delivery for detected clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get cwd: %w", err)
			}
			if config.Exists(root) && !offline {
				if cfg, err := config.Load(root); err == nil && cfg.Mode == config.ModeClient {
					if warn := softPull(cmd.Context(), cfg, pullTimeout); warn != "" {
						logx.L().Warn("sync skipped; activating from local cache", "err", warn)
						if !silent {
							fmt.Fprintf(os.Stderr, "sync skipped: %s; generating from local space\n", warn)
						}
						if requireSync {
							return fmt.Errorf("sync required: %s", warn)
						}
					}
				}
			}
			logx.L().Info("activate", "space_root", root, "target", cwd, "project", project)
			_, err = entrypoint.Generate(entrypoint.Options{
				SpaceRoot: root,
				TargetDir: cwd,
				Project:   project,
				Silent:    silent,
			})
			if err != nil {
				return err
			}
			// Remember where this project's code lives. activate is the only
			// moment contextd is ever told, and until now it forgot immediately:
			// the space could describe a project without knowing where on disk it
			// was, so a document mentioning ./scripts/deploy.sh could not be
			// checked against anything.
			recordProjectAnchor(root, cwd, project)
			// Session-start delivery: wire detected client slots (Claude hook, Cursor rules, …).
			if err := applySessionStartPlugins(root, cwd, project, silent); err != nil {
				logx.L().Warn("session-start plugins", "err", err)
				if !silent {
					fmt.Fprintf(os.Stderr, "session-start plugins: %v\n", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "active project name under projects/")
	cmd.Flags().BoolVar(&silent, "silent", false, "suppress stdout (logs still go to stderr)")
	cmd.Flags().BoolVar(&offline, "offline", false, "skip pull even in client mode")
	cmd.Flags().DurationVar(&pullTimeout, "pull-timeout", 2*time.Second, "max time to wait for soft-pull")
	cmd.Flags().BoolVar(&requireSync, "require-sync", false, "fail if soft-pull cannot reach the server")
	return cmd
}

// StatusReport is the structured form of `contextd status`.
type StatusReport struct {
	SpaceRoot string        `json:"space_root" yaml:"space_root"`
	Exists    bool          `json:"exists" yaml:"exists"`
	Mode      string        `json:"mode" yaml:"mode"`
	Config    string        `json:"config,omitempty" yaml:"config,omitempty"`
	Identity  string        `json:"identity,omitempty" yaml:"identity,omitempty"`
	Role      string        `json:"role,omitempty" yaml:"role,omitempty"`
	Template  string        `json:"template,omitempty" yaml:"template,omitempty"`
	Backend   string        `json:"backend,omitempty" yaml:"backend,omitempty"`
	Server    string        `json:"server,omitempty" yaml:"server,omitempty"`
	Space     string        `json:"space,omitempty" yaml:"space,omitempty"`
	LastHead  string        `json:"last_head,omitempty" yaml:"last_head,omitempty"`
	Missing   []string      `json:"missing" yaml:"missing"`
	Projects  []string      `json:"projects" yaml:"projects"`
	Graph     *GraphSummary `json:"graph,omitempty" yaml:"graph,omitempty"`
}

// GraphSummary is the map's shape, surfaced in status so a space that has quietly
// come apart — orphaned documents, links pointing at nothing — is visible without
// anyone going looking for it.
type GraphSummary struct {
	Nodes   int `json:"nodes" yaml:"nodes"`
	Links   int `json:"links" yaml:"links"`
	Orphans int `json:"orphans" yaml:"orphans"`
	Broken  int `json:"broken" yaml:"broken"`
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show context space status",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			st, err := space.Inspect(root)
			if err != nil {
				return err
			}
			rep := StatusReport{
				SpaceRoot: st.SpaceRoot,
				Exists:    st.Exists,
				Mode:      string(config.DetectMode()),
				Missing:   st.Missing,
				Projects:  st.Projects,
			}
			if rep.Missing == nil {
				rep.Missing = []string{}
			}
			if rep.Projects == nil {
				rep.Projects = []string{}
			}
			if st.Exists {
				if config.Exists(root) {
					if cfg, err := config.Load(root); err == nil {
						rep.Config = config.Path(root)
						rep.Identity = cfg.Identity.Name
						rep.Role = cfg.Identity.Role
						rep.Template = cfg.Template
						rep.Backend = orDefault(cfg.Backend.Driver, "local")
						if cfg.Mode == config.ModeClient {
							rep.Server = cfg.Server.URL
							rep.Space = cfg.Server.Space
							rep.LastHead = cfg.Sync.LastHead
						}
					}
				} else if st.IdentityName != "" {
					rep.Identity = st.IdentityName
				}
			}

			if g, err := loadGraph(); err == nil {
				rep.Graph = &GraphSummary{
					Nodes:   len(g.Nodes),
					Links:   len(g.Edges) - len(g.Broken),
					Orphans: len(g.Orphans),
					Broken:  len(g.Broken),
				}
			}

			return emit(cmd.OutOrStdout(), rep, func(w io.Writer) error {
				fmt.Fprintf(w, "space_root: %s\n", rep.SpaceRoot)
				fmt.Fprintf(w, "exists:     %v\n", rep.Exists)
				fmt.Fprintf(w, "mode:       %s\n", rep.Mode)
				if !rep.Exists {
					fmt.Fprintf(w, "hint:       run contextd init\n")
					return nil
				}
				if rep.Config != "" {
					fmt.Fprintf(w, "config:     %s\n", rep.Config)
					fmt.Fprintf(w, "identity:   %s (%s)\n", rep.Identity, rep.Role)
					fmt.Fprintf(w, "template:   %s\n", rep.Template)
					fmt.Fprintf(w, "backend:    %s\n", rep.Backend)
					if rep.Server != "" {
						fmt.Fprintf(w, "server:     %s\n", rep.Server)
						fmt.Fprintf(w, "space:      %s\n", rep.Space)
						fmt.Fprintf(w, "last_head:  %s\n", rep.LastHead)
					}
				} else if rep.Identity != "" {
					fmt.Fprintf(w, "identity:   %s\n", rep.Identity)
				}
				if len(rep.Missing) > 0 {
					fmt.Fprintf(w, "missing:    %s\n", strings.Join(rep.Missing, ", "))
				} else {
					fmt.Fprintf(w, "missing:    (none)\n")
				}
				if len(rep.Projects) == 0 {
					fmt.Fprintf(w, "projects:   (none)\n")
				} else {
					fmt.Fprintf(w, "projects:   %s\n", strings.Join(rep.Projects, ", "))
				}
				if g := rep.Graph; g != nil {
					line := fmt.Sprintf("%d nodes, %d links", g.Nodes, g.Links)
					if g.Orphans > 0 {
						line += fmt.Sprintf(", %d orphans", g.Orphans)
					}
					if g.Broken > 0 {
						line += fmt.Sprintf(", %d broken", g.Broken)
					}
					fmt.Fprintf(w, "graph:      %s\n", line)
				}
				return nil
			})
		},
	}
}

func newIndexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Manage the space index",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "update",
		Short: "Regenerate space-index.md from projects/ and key files",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			if err := space.UpdateIndex(root); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated %s/space-index.md\n", root)
			return nil
		},
	})
	return cmd
}

func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Browse context-space templates",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List templates in the public catalog (contextverse-templates)",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := templatepkg.List("", "", nil)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no templates found)")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Catalog: github.com/%s\n\n", templatepkg.DefaultRepo)
			for _, e := range entries {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", e.Name)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nUse: contextd init solo --template <name>\n")
			return nil
		},
	})
	return cmd
}

func newSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "space",
		Short: "Manage context spaces (local seed; create/list/show/delete on a server)",
		Long: `Manage context spaces.

  seed                    re-seed your own space from a template (solo/client)
  list · show · create · delete
                          manage the spaces of a server on this machine
                          (uses --server-dir; same core as the API and Web UI)`,
	}
	cmd.AddCommand(newSpaceListCmd())
	cmd.AddCommand(newSpaceShowCmd())
	cmd.AddCommand(newSpaceCreateCmd())
	cmd.AddCommand(newSpaceDeleteCmd())

	var (
		templateName string
		templatePath string
		force        bool
		refreshTpl   bool
	)
	seed := &cobra.Command{
		Use:   "seed",
		Short: "Re-seed space files from a template (keeps identity/me.md)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			if !force {
				if _, err := os.Stat(filepath.Join(root, "context-entry.md")); err == nil {
					return fmt.Errorf("space already has files at %s (pass --force to overwrite from template; identity/me.md is kept)", root)
				}
			}
			if err := space.Create(space.CreateOptions{
				SpaceRoot:       root,
				TemplateName:    templateName,
				TemplatePath:    templatePath,
				RefreshTemplate: refreshTpl,
				Force:           true,
				SkipIdentity:    true,
			}); err != nil {
				return err
			}
			_ = os.Remove(filepath.Join(root, "template.yaml"))
			_ = os.Remove(filepath.Join(root, "TEMPLATE.md"))
			if err := space.UpdateIndex(root); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Seeded %s from template %s\n", root, orDefault(templateName, "solo-default"))
			return nil
		},
	}
	seed.Flags().StringVar(&templateName, "template", "solo-default", "template name from catalog")
	seed.Flags().StringVar(&templatePath, "template-path", "", "local template directory")
	seed.Flags().BoolVar(&refreshTpl, "refresh-template", false, "re-fetch catalog template")
	seed.Flags().BoolVar(&force, "force", false, "overwrite existing space files (keeps identity)")
	cmd.AddCommand(seed)
	return cmd
}

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server for AI clients (Claude, Cursor, …)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the ContextVerse MCP server on stdio (reads the local space)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			return mcpserver.Run(cmd.Context(), mcpserver.Options{SpaceRoot: root})
		},
	})
	return cmd
}

func askLine(in *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stdout, "? %s [%s]: ", label, def)
	} else {
		fmt.Fprintf(os.Stdout, "? %s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// recordProjectAnchor persists where a project was activated, so the context
// graph can resolve a document's reference to ./scripts/deploy.sh against real
// files instead of taking the path on faith.
//
// Best-effort throughout: activate has already done its job by this point, and
// failing to note a path is not a reason to report that wiring the AI tools
// failed.
func recordProjectAnchor(root, cwd, project string) {
	if project == "" {
		project = plugins.ResolveProject(root, cwd)
	}
	if project == "" {
		return // not inside a known project; nothing to anchor
	}
	cfg, err := config.Load(root)
	if err != nil {
		return
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return
	}
	if !cfg.RecordAnchor(project, abs, time.Now().UTC()) {
		return // unchanged; do not rewrite config on every activate
	}
	if err := config.Save(cfg); err != nil {
		logx.L().Warn("record project anchor", "project", project, "path", abs, "err", err)
		return
	}
	logx.L().Info("project anchored", "project", project, "path", abs)
}
