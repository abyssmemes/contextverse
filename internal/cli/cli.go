package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/entrypoint"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/mcpserver"
	"github.com/abyssmemes/contextverse/internal/space"
	templatepkg "github.com/abyssmemes/contextverse/internal/template"
	"github.com/abyssmemes/contextverse/internal/version"
)

var (
	flagDebug     bool
	flagSpaceRoot string
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
	root.PersistentFlags().StringVar(&flagSpaceRoot, "dir", "", "context space root (default: ~/.context)")
	root.PersistentFlags().StringVar(&flagServerDir, "server-dir", "", "server data directory (default: ~/.contextverse-server)")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newActivateCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newIndexCmd())
	root.AddCommand(newTemplateCmd())
	root.AddCommand(newSpaceCmd())
	root.AddCommand(newBackendCmd())
	root.AddCommand(newHistoryCmd())
	root.AddCommand(newFileCmd())
	root.AddCommand(newServerCmd())
	root.AddCommand(newUserCmd())
	root.AddCommand(newAuthCmd())
	root.AddCommand(newPolicyCmd())
	root.AddCommand(newACLCmd())
	root.AddCommand(newContextCmd())
	root.AddCommand(newPluginCmd())
	root.AddCommand(newTUICmd())
	root.AddCommand(newPullCmd())
	root.AddCommand(newPushCmd())
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newAuditCmd())
	root.AddCommand(newWebhooksCmd())
	root.AddCommand(newMCPCmd())
	return root
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
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a ContextVerse installation",
		Long:  "Create and configure a context space. Solo is available now; server and client land with sync (Phase 2).",
	}
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
				name = prompt(in, "Your name", name)
				role = prompt(in, "Your role", role)
				language = prompt(in, "Preferred language", orDefault(language, "English"))
				tools = prompt(in, "Tools you use", tools)
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

			if err := space.Create(space.CreateOptions{
				SpaceRoot:       root,
				TemplateName:    templateName,
				TemplatePath:    templatePath,
				RefreshTemplate: refreshTpl,
				Identity: space.IdentityFields{
					Name:     name,
					Role:     role,
					Language: language,
					Tools:    tools,
				},
				Force: force,
			}); err != nil {
				return err
			}

			if err := space.UpdateIndex(root); err != nil {
				return err
			}

			cfg := &config.Config{
				Mode:      config.ModeSolo,
				SpaceRoot: root,
				Identity: config.Identity{
					Name:     name,
					Role:     role,
					Language: language,
				},
				Template: orDefault(templateName, "solo-default"),
				Backend:  config.Backend{Driver: "local"},
			}
			if templatePath != "" {
				cfg.Template = templatePath
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			logx.L().Info("solo init complete", "space_root", root, "mode", cfg.Mode)

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
			mode := config.DetectMode()
			fmt.Fprintf(cmd.OutOrStdout(), "space_root: %s\n", st.SpaceRoot)
			fmt.Fprintf(cmd.OutOrStdout(), "exists:     %v\n", st.Exists)
			fmt.Fprintf(cmd.OutOrStdout(), "mode:       %s\n", mode)
			if !st.Exists {
				fmt.Fprintf(cmd.OutOrStdout(), "hint:       run contextd init solo\n")
				return nil
			}
			if config.Exists(root) {
				if cfg, err := config.Load(root); err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "config:     %s\n", config.Path(root))
					fmt.Fprintf(cmd.OutOrStdout(), "identity:   %s (%s)\n", cfg.Identity.Name, cfg.Identity.Role)
					fmt.Fprintf(cmd.OutOrStdout(), "template:   %s\n", cfg.Template)
					driver := cfg.Backend.Driver
					if driver == "" {
						driver = "local"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "backend:    %s\n", driver)
					if cfg.Mode == config.ModeClient {
						fmt.Fprintf(cmd.OutOrStdout(), "server:     %s\n", cfg.Server.URL)
						fmt.Fprintf(cmd.OutOrStdout(), "space:      %s\n", cfg.Server.Space)
						fmt.Fprintf(cmd.OutOrStdout(), "last_head:  %s\n", cfg.Sync.LastHead)
					}
				}
			} else if st.IdentityName != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "identity:   %s\n", st.IdentityName)
			}
			if len(st.Missing) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "missing:    %s\n", strings.Join(st.Missing, ", "))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "missing:    (none)\n")
			}
			if len(st.Projects) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "projects:   (none)\n")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "projects:   %s\n", strings.Join(st.Projects, ", "))
			}
			return nil
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
		Short: "Manage the context space",
	}

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

func prompt(in *bufio.Reader, label, def string) string {
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
