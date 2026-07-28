package cli

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/audit"
)

func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Query the server audit log",
		Long:  `Read append-only JSONL under <server-dir>/audit/. Requires sys/audit capability (admin or auditor).`,
	}
	cmd.AddCommand(newAuditListCmd())
	cmd.AddCommand(newAuditExportCmd())
	cmd.AddCommand(newAuditStatsCmd())
	cmd.AddCommand(newAuditVerifyCmd())
	return cmd
}

func newAuditVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Check the audit log hash chain for tampering",
		Long:  `Recomputes every record hash and its link to the previous record. Exits non-zero on the first break.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lg, err := openAuditLogger()
			if err != nil {
				return err
			}
			n, err := lg.Verify()
			if err != nil {
				return err
			}
			return emit(cmd.OutOrStdout(), AuditVerification{Intact: true, Entries: n}, func(w io.Writer) error {
				fmt.Fprintf(w, "audit chain intact: %d entries verified\n", n)
				return nil
			})
		},
	}
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

// AuditEntry is one audit record, as `audit list` reports it.
type AuditEntry struct {
	Time   string `json:"time" yaml:"time"`
	Actor  string `json:"actor" yaml:"actor"`
	Action string `json:"action" yaml:"action"`
	Space  string `json:"space,omitempty" yaml:"space,omitempty"`
	Target string `json:"target,omitempty" yaml:"target,omitempty"`
	Result string `json:"result" yaml:"result"`
}

// AuditActionCount and AuditStats summarise activity. ByAction is a sorted
// slice rather than a map: a map has no order, and callers comparing two runs
// need one.
type AuditActionCount struct {
	Action string `json:"action" yaml:"action"`
	Count  int    `json:"count" yaml:"count"`
}

// AuditStats is the summary `audit stats` reports.
type AuditStats struct {
	Entries  int                `json:"entries" yaml:"entries"`
	Actors   int                `json:"actors" yaml:"actors"`
	Failed   int                `json:"failed" yaml:"failed"`
	ByAction []AuditActionCount `json:"by_action" yaml:"by_action"`
}

// AuditVerification is the verdict from `audit verify`. Intact is always true
// in a successful payload — a broken chain is an error, not a field — but it is
// present so a script can assert on it rather than on the absence of output.
type AuditVerification struct {
	Intact  bool `json:"intact" yaml:"intact"`
	Entries int  `json:"entries" yaml:"entries"`
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
			out := make([]AuditEntry, 0, len(entries))
			for _, e := range entries {
				out = append(out, AuditEntry{
					Time:   e.Timestamp.UTC().Format(time.RFC3339),
					Actor:  e.Actor.Username,
					Action: e.Action,
					Space:  e.Space,
					// Untruncated: the table shortens long targets to stay
					// readable, but a truncated path in a structured payload is
					// a path a script cannot use.
					Target: e.Target,
					Result: e.Result,
				})
			}
			return emit(cmd.OutOrStdout(), out, func(wr io.Writer) error {
				w := tabwriter.NewWriter(wr, 0, 4, 2, ' ', 0)
				fmt.Fprintln(w, "TIME\tACTOR\tACTION\tSPACE\tTARGET\tRESULT")
				for _, e := range out {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
						e.Time, e.Actor, e.Action, e.Space, truncate(e.Target, 40), e.Result)
				}
				if err := w.Flush(); err != nil {
					return err
				}
				fmt.Fprintf(wr, "\n%d entries (dir %s)\n", len(out), l.Dir())
				return nil
			})
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
			report := AuditStats{Entries: st.Entries, Actors: st.Actors, Failed: st.Failed}
			// Sorted, because ranging a map gives a different order every run:
			// two identical invocations printed different output, which makes
			// the command useless to diff and unnerving to read.
			for a := range st.ByAction {
				report.ByAction = append(report.ByAction, AuditActionCount{Action: a, Count: st.ByAction[a]})
			}
			sort.Slice(report.ByAction, func(i, j int) bool {
				if report.ByAction[i].Count != report.ByAction[j].Count {
					return report.ByAction[i].Count > report.ByAction[j].Count
				}
				return report.ByAction[i].Action < report.ByAction[j].Action
			})

			return emit(cmd.OutOrStdout(), report, func(w io.Writer) error {
				fmt.Fprintf(w, "Entries:  %d\n", report.Entries)
				fmt.Fprintf(w, "Actors:   %d\n", report.Actors)
				fmt.Fprintf(w, "Failed:   %d\n", report.Failed)
				fmt.Fprintln(w, "By action:")
				for _, a := range report.ByAction {
					fmt.Fprintf(w, "  %-28s %d\n", a.Action, a.Count)
				}
				return nil
			})
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
