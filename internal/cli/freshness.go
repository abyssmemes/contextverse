package cli

import (
	"fmt"
	"io"
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
		Short: "Track which context has gone stale and who owns it",
		Long:  `Scan markdown frontmatter for stale-after. Use nag to emit freshness.stale webhooks on the server.`,
	}
	cmd.AddCommand(newFreshnessCheckCmd())
	cmd.AddCommand(newFreshnessNagCmd())
	cmd.AddCommand(newFreshnessValidateCmd())
	return cmd
}

// FreshnessEntry is one document's freshness, as `freshness check --json`
// reports it.
type FreshnessEntry struct {
	Path          string `json:"path" yaml:"path"`
	Stale         bool   `json:"stale" yaml:"stale"`
	LastValidated string `json:"last_validated" yaml:"last_validated"`
	StaleAfter    string `json:"stale_after,omitempty" yaml:"stale_after,omitempty"`
	Owner         string `json:"owner,omitempty" yaml:"owner,omitempty"`
}

// FreshnessReport is the whole scan. The counts are in the payload rather than
// left for the caller to derive, because "how many are stale" is the question
// almost every caller actually has.
type FreshnessReport struct {
	Space string           `json:"space" yaml:"space"`
	Total int              `json:"total" yaml:"total"`
	Stale int              `json:"stale" yaml:"stale"`
	Files []FreshnessEntry `json:"files" yaml:"files"`
}

// staleAfter renders a window the way documents declare it. The frontmatter
// says "90d"; time.Duration.String() says "2160h0m0s", which is the same fact
// in a form nobody writes down.
func staleAfter(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int64(d/(24*time.Hour)))
	}
	return d.String()
}

func newFreshnessCheckCmd() *cobra.Command {
	var serverSide bool
	var spaceName string
	var failOnStale bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "List files with freshness metadata (stale highlighted)",
		Long: `Scan the space for documents whose stale-after window has passed.

With --fail-on-stale the command exits non-zero when anything is stale, so it
can gate a build:

    contextd freshness check --fail-on-stale

Without it the command reports and exits 0, which is what you want when a human
is reading the table.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, space, err := freshnessRoot(serverSide, spaceName)
			if err != nil {
				return err
			}
			all, err := freshness.ScanDir(root, time.Now().UTC())
			if err != nil {
				return err
			}

			report := FreshnessReport{Space: space, Total: len(all)}
			for _, m := range all {
				if m.Stale {
					report.Stale++
				}
				report.Files = append(report.Files, FreshnessEntry{
					Path:          m.Path,
					Stale:         m.Stale,
					LastValidated: m.LastValidated.Format("2006-01-02"),
					StaleAfter:    staleAfter(m.StaleAfter),
					Owner:         m.Owner,
				})
			}

			if err := emit(cmd.OutOrStdout(), report, func(w io.Writer) error {
				tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "STALE\tPATH\tLAST-VALIDATED\tSTALE-AFTER\tOWNER")
				for _, f := range report.Files {
					mark := ""
					if f.Stale {
						mark = "yes"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
						mark, f.Path, f.LastValidated, f.StaleAfter, f.Owner)
				}
				return tw.Flush()
			}); err != nil {
				return err
			}

			if report.Stale > 0 {
				// The count goes to stderr so it never lands in a piped table,
				// and is in the structured payload for anyone parsing.
				fmt.Fprintf(cmd.ErrOrStderr(), "\n%d stale file(s) in %s\n", report.Stale, space)
				if failOnStale {
					return &ExitError{Code: ExitUsage, Err: fmt.Errorf("%d stale file(s) in %s", report.Stale, space)}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&serverSide, "server", false, "scan a server space under --server-dir")
	cmd.Flags().StringVar(&spaceName, "space", "", "space name (with --server; default from config)")
	cmd.Flags().BoolVar(&failOnStale, "fail-on-stale", false, "exit non-zero if any file is stale (for CI)")
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
			policy := webhooks.TargetPolicy{AllowPrivate: cfg.Webhooks.AllowPrivateTargets}
			st.Policy = policy
			d := webhooks.NewDispatcher(st)
			d.SetPolicy(policy)
			for _, m := range stale {
				d.EmitSync(webhooks.Event{
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
