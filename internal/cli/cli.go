package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/entrypoint"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/space"
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

	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newActivateCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newIndexCmd())
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
	cmd.AddCommand(&cobra.Command{
		Use:   "server",
		Short: "Initialize a ContextVerse server (not yet implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("init server is not implemented yet — arrives with Phase 2 (sync)")
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "client",
		Short: "Initialize a ContextVerse client (not yet implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("init client is not implemented yet — arrives with Phase 2 (sync)")
		},
	})
	return cmd
}

func newInitSoloCmd() *cobra.Command {
	var (
		name         string
		role         string
		language     string
		tools        string
		templateName string
		templatePath string
		nonInteractive bool
		force        bool
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
				SpaceRoot:    root,
				TemplateName: templateName,
				TemplatePath: templatePath,
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
	cmd.Flags().StringVar(&templateName, "template", "solo-default", "embedded template name")
	cmd.Flags().StringVar(&templatePath, "template-path", "", "path to a local template directory (overrides --template)")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "do not prompt; require flags")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing space")
	return cmd
}

func newActivateCmd() *cobra.Command {
	var (
		project string
		silent  bool
	)
	cmd := &cobra.Command{
		Use:   "activate",
		Short: "Generate AI entry points (CLAUDE.md, .cursor/rules) in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get cwd: %w", err)
			}
			logx.L().Info("activate", "space_root", root, "target", cwd, "project", project)
			_, err = entrypoint.Generate(entrypoint.Options{
				SpaceRoot: root,
				TargetDir: cwd,
				Project:   project,
				Silent:    silent,
			})
			return err
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "active project name under projects/")
	cmd.Flags().BoolVar(&silent, "silent", false, "suppress stdout (logs still go to stderr)")
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
