package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/authz"
)

func newACLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acl",
		Short: "Per-user path ACL (deny-wins exceptions)",
		Long: `Manage auth/acl.yaml rules attached to individual users.

Examples:
  contextd acl deny bob 'spaces/{{default}}/files/team/principles.md'
  contextd acl allow alice 'spaces/{{default}}/files/projects/secret/*' --cap read,list
  contextd acl list --user bob
  contextd policy test --user bob --path 'spaces/team/files/team/principles.md' --cap update`,
	}
	cmd.AddCommand(newACLListCmd())
	cmd.AddCommand(newACLAllowCmd())
	cmd.AddCommand(newACLDenyCmd())
	cmd.AddCommand(newACLUnsetCmd())
	return cmd
}

func newACLListCmd() *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List per-user ACL rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := openPolicyEngine()
			if err != nil {
				return err
			}
			users := []string{user}
			if user == "" {
				users = eng.ListACLUsers()
			}
			if len(users) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no per-user ACL rules)")
				return nil
			}
			for _, u := range users {
				rules := eng.UserRules(u)
				fmt.Fprintf(cmd.OutOrStdout(), "%s:\n", u)
				if len(rules) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "  (none)")
					continue
				}
				for _, r := range rules {
					caps := make([]string, len(r.Capabilities))
					for i, c := range r.Capabilities {
						caps[i] = string(c)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  - path: %s\n    capabilities: [%s]\n", r.Path, strings.Join(caps, ", "))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "filter to one user")
	return cmd
}

func newACLAllowCmd() *cobra.Command {
	var caps string
	cmd := &cobra.Command{
		Use:   "allow <user> <path>",
		Short: "Grant capabilities on a path for one user",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := openPolicyEngine()
			if err != nil {
				return err
			}
			list := parseCaps(caps)
			if len(list) == 0 {
				list = []authz.Capability{authz.CapRead, authz.CapList}
			}
			return eng.AddUserRule(args[0], authz.Rule{Path: args[1], Capabilities: list})
		},
	}
	cmd.Flags().StringVar(&caps, "cap", "read,list", "comma-separated capabilities")
	return cmd
}

func newACLDenyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deny <user> <path>",
		Short: "Explicit deny on a path (overrides role grants)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := openPolicyEngine()
			if err != nil {
				return err
			}
			return eng.AddUserRule(args[0], authz.Rule{
				Path:         args[1],
				Capabilities: []authz.Capability{authz.CapDeny},
			})
		},
	}
}

func newACLUnsetCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "unset <user> [path]",
		Short: "Remove a path rule (or --all rules for user)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := openPolicyEngine()
			if err != nil {
				return err
			}
			if all || len(args) == 1 {
				return eng.SetUserRules(args[0], nil)
			}
			return eng.RemoveUserRulePath(args[0], args[1])
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "clear all rules for user")
	return cmd
}

func parseCaps(s string) []authz.Capability {
	var out []authz.Capability
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, authz.Capability(p))
	}
	return out
}
