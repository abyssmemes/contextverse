package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/plugins"
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
			out, err := plugins.Inject(format, root, cwd, project)
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
	return cmd
}

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Wire AI client session-start slots (client-integration templates)",
	}
	cmd.AddCommand(newPluginListCmd())
	cmd.AddCommand(newPluginInstallCmd())
	return cmd
}

func newPluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List known client-integration templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			cat, err := plugins.DefaultCatalog("")
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
			for _, in := range cat {
				mark := ""
				if how, ok := detected[in.ID]; ok {
					mark = "\tdetected(" + how + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s%s\n", in.ID, in.Mechanism, in.Display, mark)
			}
			return nil
		},
	}
}

func newPluginInstallCmd() *cobra.Command {
	var (
		project        string
		nonInteractive bool
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
			cat, err := plugins.DefaultCatalog("")
			if err != nil {
				return err
			}
			if len(args) == 0 {
				interactive := !nonInteractive
				if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
					interactive = false
				}
				results, err := plugins.ApplyDetected(cat, vars, plugins.ApplyOpts{Interactive: interactive})
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
	cat, err := plugins.DefaultCatalog("")
	if err != nil {
		return err
	}
	interactive := !silent
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		interactive = false
	}
	results, err := plugins.ApplyDetected(cat, vars, plugins.ApplyOpts{Interactive: interactive})
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
