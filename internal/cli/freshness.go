package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/freshness"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
	"github.com/abyssmemes/contextverse/internal/webhooks"
)

func newFreshnessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "freshness",
		Short: "Check stale-after / last-validated metadata (Phase 3)",
		Long:  `Scan markdown frontmatter for stale-after. Use nag to emit freshness.stale webhooks on the server.`,
	}
	cmd.AddCommand(newFreshnessCheckCmd())
	cmd.AddCommand(newFreshnessNagCmd())
	cmd.AddCommand(newFreshnessValidateCmd())
	return cmd
}

func newFreshnessCheckCmd() *cobra.Command {
	var serverSide bool
	var spaceName string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "List files with freshness metadata (stale highlighted)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, space, err := freshnessRoot(serverSide, spaceName)
			if err != nil {
				return err
			}
			all, err := freshness.ScanDir(root, time.Now().UTC())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "STALE\tPATH\tLAST-VALIDATED\tSTALE-AFTER\tOWNER")
			for _, m := range all {
				mark := ""
				if m.Stale {
					mark = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					mark, m.Path, m.LastValidated.Format("2006-01-02"), m.StaleAfter, m.Owner)
			}
			_ = tw.Flush()
			stale := freshness.StaleOnly(all)
			if len(stale) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "\n%d stale file(s) in %s\n", len(stale), space)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&serverSide, "server", false, "scan a server space under --server-dir")
	cmd.Flags().StringVar(&spaceName, "space", "", "space name (with --server; default from config)")
	return cmd
}

func newFreshnessNagCmd() *cobra.Command {
	var serverSide bool
	var spaceName string
	cmd := &cobra.Command{
		Use:   "nag",
		Short: "Emit freshness.stale webhooks for stale files (server)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !serverSide {
				return fmt.Errorf("nag requires --server (emits webhooks from server data dir)")
			}
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			cfg, err := config.LoadServer(dir)
			if err != nil {
				return err
			}
			space := spaceName
			if space == "" {
				space = cfg.Defaults.Space
			}
			if space == "" {
				return fmt.Errorf("--space required")
			}
			svc := &spacesvc.Service{DataDir: dir, Backend: cfg.Backend}
			root := svc.SpaceRoot(space)
			all, err := freshness.ScanDir(root, time.Now().UTC())
			if err != nil {
				return err
			}
			stale := freshness.StaleOnly(all)
			st, err := webhooks.Open(dir)
			if err != nil {
				return err
			}
			d := webhooks.NewDispatcher(st)
			for _, m := range stale {
				d.Emit(webhooks.Event{
					Type:  "freshness.stale",
					Space: space,
					Scope: m.Path,
					Actor: "freshness-nag",
					Data: map[string]any{
						"path":           m.Path,
						"last_validated": m.LastValidated.Format("2006-01-02"),
						"owner":          m.Owner,
					},
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "nagged %d stale file(s) in space %s\n", len(stale), space)
			return nil
		},
	}
	cmd.Flags().BoolVar(&serverSide, "server", false, "use server data dir")
	cmd.Flags().StringVar(&spaceName, "space", "", "space name")
	return cmd
}

func newFreshnessValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [path...]",
		Short: "Stamp last-validated on local markdown files",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			for _, a := range args {
				p := a
				if !filepath.IsAbs(p) {
					p = filepath.Join(root, filepath.FromSlash(a))
				}
				raw, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				out, err := freshness.StampValidated(raw, now)
				if err != nil {
					return err
				}
				if err := os.WriteFile(p, out, 0o644); err != nil {
					return err
				}
				rel, _ := filepath.Rel(root, p)
				fmt.Fprintf(cmd.OutOrStdout(), "validated %s\n", filepath.ToSlash(rel))
			}
			return nil
		},
	}
	return cmd
}

func freshnessRoot(serverSide bool, spaceName string) (root, space string, err error) {
	if !serverSide {
		root, err = resolveSpaceRoot()
		if err != nil {
			return "", "", err
		}
		return root, "local", nil
	}
	dir, err := resolveServerDir()
	if err != nil {
		return "", "", err
	}
	cfg, err := config.LoadServer(dir)
	if err != nil {
		return "", "", err
	}
	space = spaceName
	if space == "" {
		space = cfg.Defaults.Space
	}
	if space == "" {
		return "", "", fmt.Errorf("--space required with --server")
	}
	svc := &spacesvc.Service{DataDir: dir, Backend: cfg.Backend}
	return svc.SpaceRoot(space), space, nil
}