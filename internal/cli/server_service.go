package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/winsvc"
)

func newServerServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Windows service install/start/stop (Windows only)",
		Long: `Manage contextd as a native Windows service (SCM).

Requires an elevated shell (Administrator) for install/uninstall/start/stop.
On Linux/macOS these commands error with a pointer to systemd/launchd samples.`,
	}
	cmd.AddCommand(newServerServiceInstallCmd())
	cmd.AddCommand(newServerServiceUninstallCmd())
	cmd.AddCommand(newServerServiceStartCmd())
	cmd.AddCommand(newServerServiceStopCmd())
	return cmd
}

func newServerServiceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install ContextVerse Windows service for this server-dir",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if err := winsvc.Install("", dir); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed Windows service %q\n  server-dir: %s\n  start: contextd server service start\n", winsvc.Name, dir)
			return nil
		},
	}
}

func newServerServiceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the ContextVerse Windows service",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = winsvc.Stop() // best-effort
			if err := winsvc.Uninstall(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "uninstalled Windows service %q\n", winsvc.Name)
			return nil
		},
	}
}

func newServerServiceStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the ContextVerse Windows service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := winsvc.Start(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "started Windows service %q\n", winsvc.Name)
			return nil
		},
	}
}

func newServerServiceStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the ContextVerse Windows service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := winsvc.Stop(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stopped Windows service %q\n", winsvc.Name)
			return nil
		},
	}
}
