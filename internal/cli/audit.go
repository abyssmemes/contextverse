package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/audit"
)

func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Query the server audit log (Phase 3)",
		Long:  `Read append-only JSONL under <server-dir>/audit/. Requires sys/audit capability (admin or auditor).`,
	}
	cmd.AddCommand(newAuditListCmd())
	cmd.AddCommand(newAuditExportCmd())
	cmd.AddCommand(newAuditStatsCmd())
	return cmd
}

func openAuditLogger() (*audit.Logger, error) {
	dir, err := resolveServerDir()
	if err != nil {
		return nil, err
	}
	return audit.Open(dir)
}

func auditFilterFlags(cmd *cobra.Command) (actor, action, space, result, since string, limit int) {
	actor, _ = cmd.Flags().GetString("actor")
	action, _ = cmd.Flags().GetString("action")
	space, _ = cmd.Flags().GetString("space")
	result, _ = cmd.Flags().GetString("result")
	since, _ = cmd.Flags().GetString("since")
	limit, _ = cmd.Flags().GetInt("limit")
	return
}

func buildAuditFilter(cmd *cobra.Command) (audit.Filter, error) {
	actor, action, space, result, since, limit := auditFilterFlags(cmd)
	f := audit.Filter{Actor: actor, Action: action, Space: space, Result: result, Limit: limit}
	if since != "" {
		ts, err := audit.ParseSince(since)
		if err != nil {
			return f, err
		}
		f.Since = ts
	}
	return f, nil
}

func addAuditFilterFlags(cmd *cobra.Command) {
	cmd.Flags().String("actor", "", "filter by username")
	cmd.Flags().String("action", "", "filter by action (substring or *glob*)")
	cmd.Flags().String("space", "", "filter by space")
	cmd.Flags().String("result", "", "success|denied|error")
	cmd.Flags().String("since", "", "24h, 7d, RFC3339, or YYYY-MM-DD")
	cmd.Flags().Int("limit", 50, "max entries (list); 0=default")
}

func newAuditListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent audit entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := openAuditLogger()
			if err != nil {
				return err
			}
			f, err := buildAuditFilter(cmd)
			if err != nil {
				return err
			}
			entries, err := l.Query(f)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "TIME\tACTOR\tACTION\tSPACE\tTARGET\tRESULT")
			for _, e := range entries {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
					e.Actor.Username,
					e.Action,
					e.Space,
					truncate(e.Target, 40),
					e.Result,
				)
			}
			_ = w.Flush()
			fmt.Fprintf(cmd.OutOrStdout(), "\n%d entries (dir %s)\n", len(entries), l.Dir())
			return nil
		},
	}
	addAuditFilterFlags(cmd)
	return cmd
}

func newAuditExportCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export audit log (jsonl or csv) to stdout",
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := openAuditLogger()
			if err != nil {
				return err
			}
			f, err := buildAuditFilter(cmd)
			if err != nil {
				return err
			}
			f.Limit = -1
			switch format {
			case "csv":
				return l.ExportCSV(cmd.OutOrStdout(), f)
			default:
				return l.ExportJSONL(cmd.OutOrStdout(), f)
			}
		},
	}
	addAuditFilterFlags(cmd)
	cmd.Flags().StringVar(&format, "format", "jsonl", "jsonl|csv")
	return cmd
}

func newAuditStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Summarize audit activity",
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := openAuditLogger()
			if err != nil {
				return err
			}
			f, err := buildAuditFilter(cmd)
			if err != nil {
				return err
			}
			st, err := l.Stats(f)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Entries:  %d\n", st.Entries)
			fmt.Fprintf(cmd.OutOrStdout(), "Actors:   %d\n", st.Actors)
			fmt.Fprintf(cmd.OutOrStdout(), "Failed:   %d\n", st.Failed)
			fmt.Fprintln(cmd.OutOrStdout(), "By action:")
			for a, n := range st.ByAction {
				fmt.Fprintf(cmd.OutOrStdout(), "  %-28s %d\n", a, n)
			}
			return nil
		},
	}
	addAuditFilterFlags(cmd)
	return cmd
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}