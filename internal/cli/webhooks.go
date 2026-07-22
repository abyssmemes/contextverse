package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/webhooks"
)

func newWebhooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhooks",
		Short: "Manage outbound webhooks (Phase 3)",
	}
	cmd.AddCommand(newWebhooksListCmd())
	cmd.AddCommand(newWebhooksAddCmd())
	cmd.AddCommand(newWebhooksDeleteCmd())
	cmd.AddCommand(newWebhooksTestCmd())
	cmd.AddCommand(newWebhooksDeadLetterCmd())
	return cmd
}

func openWebhookStore() (*webhooks.Store, error) {
	dir, err := resolveServerDir()
	if err != nil {
		return nil, err
	}
	return webhooks.Open(dir)
}

func newWebhooksListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured webhooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openWebhookStore()
			if err != nil {
				return err
			}
			list, err := st.List()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tENABLED\tEVENTS\tSPACE\tURL")
			for _, h := range list {
				ev := strings.Join(h.Events, ",")
				if ev == "" {
					ev = "*"
				}
				fmt.Fprintf(w, "%s\t%v\t%s\t%s\t%s\n", h.ID, h.Enabled, ev, h.Space, h.URL)
			}
			return w.Flush()
		},
	}
}

func newWebhooksAddCmd() *cobra.Command {
	var (
		url    string
		events string
		space  string
		secret string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a webhook (prints secret once)",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openWebhookStore()
			if err != nil {
				return err
			}
			var ev []string
			if events != "" && events != "*" {
				ev = strings.Split(events, ",")
				for i := range ev {
					ev[i] = strings.TrimSpace(ev[i])
				}
			}
			h, err := st.Upsert(webhooks.Hook{
				URL:     url,
				Events:  ev,
				Space:   space,
				Secret:  secret,
				Enabled: true,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "id:      %s\n", h.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "url:     %s\n", h.URL)
			fmt.Fprintf(cmd.OutOrStdout(), "secret:  %s\n", h.Secret)
			fmt.Fprintf(cmd.OutOrStdout(), "events:  %v\n", h.Events)
			fmt.Fprintln(cmd.OutOrStdout(), "(store the secret now — it is not shown again in list)")
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "destination URL (required)")
	cmd.Flags().StringVar(&events, "events", "*", "comma-separated event types or *")
	cmd.Flags().StringVar(&space, "space", "", "optional space filter")
	cmd.Flags().StringVar(&secret, "secret", "", "HMAC secret (generated if empty)")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newWebhooksDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a webhook",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openWebhookStore()
			if err != nil {
				return err
			}
			return st.Delete(args[0])
		},
	}
}

func newWebhooksTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <id>",
		Short: "POST a webhook.test event to the hook",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openWebhookStore()
			if err != nil {
				return err
			}
			d := webhooks.NewDispatcher(st)
			if err := d.Test(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
}

func newWebhooksDeadLetterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dead-letter",
		Short: "Show failed deliveries",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openWebhookStore()
			if err != nil {
				return err
			}
			list, err := st.ListDeadLetter(50)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(list)
		},
	}
}