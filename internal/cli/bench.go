package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/bench"
)

func newBenchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Measure how context is delivered (experimental)",
		Long: `Measure eager against lazy context delivery.

The product sends an AI its whole entry set at the start of every session.
Whether that beats letting the client fetch what it needs has never been
measured, which is why no token-efficiency claim appears anywhere in the
documentation.

This is deterministic and free: it measures whether an arm can reach the
document that answers a question, and what reaching it costs. Whether a model
then answers better is a separate question that needs a model.`,
	}
	cmd.AddCommand(newBenchContextCmd())
	return cmd
}

func newBenchContextCmd() *cobra.Command {
	var tasksPath string
	var budget, maxSteps int
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Compare context-delivery strategies against a task set",
		Example: `  # A task set is a YAML file of questions and the documents that answer them:
  #   tasks:
  #     - question: what is our deploy process
  #       answers: [projects/api/runbook.md]
  contextd bench context --tasks bench.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			ts, err := bench.LoadTasks(tasksPath)
			if err != nil {
				return err
			}
			rep, err := bench.Run(cmd.Context(), ts, bench.Options{
				Root: root, Budget: budget, MaxSteps: maxSteps,
			})
			if err != nil {
				return err
			}

			return emit(cmd.OutOrStdout(), rep, func(w io.Writer) error {
				fmt.Fprintf(w, "%d tasks against %s\n\n", rep.Tasks, rep.Space)
				tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "ARM\tREACHED\tUPFRONT\tFETCHED\tTOTAL\tPER-ANSWER")
				for _, a := range rep.Arms {
					per := "—"
					if a.Reached > 0 {
						per = fmt.Sprintf("%d", a.TokensPerAnswer())
					}
					fmt.Fprintf(tw, "%s\t%d/%d\t%d\t%d\t%d\t%s\n",
						a.Arm, a.Reached, a.Total, a.Upfront, a.Fetched, a.Upfront+a.Fetched, per)
				}
				if err := tw.Flush(); err != nil {
					return err
				}
				fmt.Fprintln(w, "\nTokens are approximate (~4 chars each), consistent across arms.")
				fmt.Fprintln(w, "Reaching the answer is necessary, not sufficient: whether a model")
				fmt.Fprintln(w, "answers better having reached it is a separate question.")
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&tasksPath, "tasks", "", "task set (YAML) — required")
	cmd.Flags().IntVar(&budget, "budget", 700, "token budget for the map arm")
	cmd.Flags().IntVar(&maxSteps, "max-steps", 3, "retrieval calls the map arm may make per question")
	_ = cmd.MarkFlagRequired("tasks")
	return cmd
}
