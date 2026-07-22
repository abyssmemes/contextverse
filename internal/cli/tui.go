package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/tui"
)

func newTUICmd() *cobra.Command {
	var asServer bool
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI (CLI wrapper)",
		Long: `Full-screen TUI over the same operations as the CLI.

Uses the full terminal (header, tabs, split panes, scrollable output).

Client/solo:
  1 Space · 2 Projects · 3 Plugins · 4 Output · ? Help
  a=activate  i=plugin install  s=status  u/U=pull/push (client)
  r=refresh  j/k=move  q=quit

Server admin (--server):
  1 Overview · 2 Spaces · 3 Users · 4 Policies · 5 Backend · 6 Output
  s=status  H=health  r=refresh  q=quit

Wish SSH (Model B): contextd tui ssh enable && server start
Host login (Model A): contextd tui login enable [--server] [--user name]`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if asServer || flagServerDir != "" {
				dir, err := resolveServerDir()
				if err != nil {
					return err
				}
				return tui.RunServer(dir)
			}
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if _, err := os.Stat(root); err != nil {
				// Soft auto-detect: if no space but a server data dir exists, open server TUI.
				if dir, serr := resolveServerDir(); serr == nil && config.ServerExists(dir) {
					fmt.Fprintf(cmd.ErrOrStderr(), "no space at %s — opening server TUI at %s\n", root, dir)
					return tui.RunServer(dir)
				}
				return fmt.Errorf("space root %s: %w (run contextd init solo, or contextd tui --server)", root, err)
			}
			return tui.Run(root, cwd)
		},
	}
	cmd.Flags().BoolVar(&asServer, "server", false, "open server admin TUI (uses --server-dir)")
	cmd.AddCommand(newTUISSHCmd())
	cmd.AddCommand(newTUILoginCmd())
	return cmd
}

func newTUISSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh",
		Short: "Configure Model B Wish SSH for the server admin TUI",
	}
	cmd.AddCommand(newTUISSHEnableCmd())
	cmd.AddCommand(newTUISSHDisableCmd())
	cmd.AddCommand(newTUISSHAddKeyCmd())
	cmd.AddCommand(newTUISSHStatusCmd())
	return cmd
}

func newTUISSHEnableCmd() *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable Wish SSH listener in server config (starts with server start)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if !config.ServerExists(dir) {
				return fmt.Errorf("server not initialized at %s", dir)
			}
			cfg, err := config.LoadServer(dir)
			if err != nil {
				return err
			}
			if listen == "" {
				listen = config.DefaultTUISSHListen
			}
			cfg.TUI.SSH.Enabled = true
			cfg.TUI.SSH.Listen = listen
			cfg.TUI.SSH.AutoLaunch = true
			if err := config.SaveServer(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "TUI SSH enabled on %s\n", listen)
			fmt.Fprintf(cmd.OutOrStdout(), "Authorized keys: %s\n", config.TUISSHAuthorizedKeysPath(dir))
			fmt.Fprintf(cmd.OutOrStdout(), "Add a key: contextd tui ssh add-key ~/.ssh/id_ed25519.pub\n")
			fmt.Fprintf(cmd.OutOrStdout(), "Then: contextd server start --server-dir %s\n", dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&listen, "listen", config.DefaultTUISSHListen, "Wish SSH listen address")
	return cmd
}

func newTUISSHDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable Wish SSH listener in server config",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if !config.ServerExists(dir) {
				return fmt.Errorf("server not initialized at %s", dir)
			}
			cfg, err := config.LoadServer(dir)
			if err != nil {
				return err
			}
			cfg.TUI.SSH.Enabled = false
			if err := config.SaveServer(cfg); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "TUI SSH disabled")
			return nil
		},
	}
}

func newTUISSHAddKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add-key [pubkey-file]",
		Short: "Append an OpenSSH public key to the TUI SSH allowlist",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			var raw []byte
			if len(args) == 0 {
				raw, err = os.ReadFile("/dev/stdin")
			} else {
				raw, err = os.ReadFile(args[0])
			}
			if err != nil {
				return err
			}
			line := strings.TrimSpace(string(raw))
			if line == "" {
				return fmt.Errorf("empty public key")
			}
			// Take first non-empty, non-comment line.
			for _, l := range strings.Split(line, "\n") {
				l = strings.TrimSpace(l)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				line = l
				break
			}
			if err := tui.AppendAuthorizedKey(dir, line); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added key to %s\n", config.TUISSHAuthorizedKeysPath(dir))
			return nil
		},
	}
}

func newTUISSHStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show TUI SSH config and key file paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if !config.ServerExists(dir) {
				return fmt.Errorf("server not initialized at %s", dir)
			}
			cfg, err := config.LoadServer(dir)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "enabled:  %v\n", cfg.TUI.SSH.Enabled)
			fmt.Fprintf(cmd.OutOrStdout(), "listen:   %s\n", cfg.TUI.SSH.Listen)
			fmt.Fprintf(cmd.OutOrStdout(), "host key: %s\n", config.TUISSHHostKeyPath(dir))
			fmt.Fprintf(cmd.OutOrStdout(), "auth keys:%s\n", config.TUISSHAuthorizedKeysPath(dir))
			if st, err := os.Stat(config.TUISSHAuthorizedKeysPath(dir)); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "auth keys size: %d bytes\n", st.Size())
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "auth keys: (missing — add-key required)")
			}
			return nil
		},
	}
}
