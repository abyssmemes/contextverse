package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/localui"
	"github.com/abyssmemes/contextverse/internal/logx"
)

func newUICmd() *cobra.Command {
	var (
		addr        string
		openBrowser bool
	)
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open a local web console for your own space",
		Long: `Serve your context space in a browser, for as long as this command runs.

It is on demand rather than always on. The console can write to your context
files and runs as you, with no account behind it — on a laptop that is a door
worth opening only while you are walking through it. The TUI (contextd tui)
covers the same ground and listens on nothing.

The console binds to loopback, mints a fresh link each run, and validates Host
and Origin so a page in another tab cannot drive it. Stop it with Ctrl-C.

To have it running all the time anyway: contextd ui install.
To serve a space to other people, you want a server: contextd init server.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			srv, err := localui.New(localui.Options{
				SpaceRoot: root,
				Addr:      addr,
				FileLog:   fl,
				Mode:      string(cfg.Mode),
				Anchors:   anchorsFrom(cfg),
			})
			if err != nil {
				return err
			}

			url := srv.URL()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\nLocal console for %s\n\n", root)
			fmt.Fprintf(out, "  %s\n\n", url)
			fmt.Fprintf(out, "The link carries a one-time key; it stops working when this command exits.\n")
			fmt.Fprintf(out, "Ctrl-C to stop.\n")
			logx.L().Info("local ui listening", "addr", srv.Addr(), "space", root)

			if openBrowser {
				go func() { _ = execOpen(url) }()
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := srv.Serve(ctx); err != nil {
				return err
			}
			fmt.Fprintln(out, "\nstopped")
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:0", "loopback address to bind (port 0 picks a free one)")
	cmd.Flags().BoolVar(&openBrowser, "open", true, "open the console in your browser")

	cmd.AddCommand(newUIInstallCmd())
	cmd.AddCommand(newUIUninstallCmd())
	return cmd
}

// The install/uninstall pair mirrors the daemon's, for people who do want the
// console running all the time. It is opt-in on purpose — see the command's
// Long text and internal/localui's package comment for why that default is not
// reversed.

func newUIInstallCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Keep the local console running (per-user service)",
		Long: `Install a per-user service that keeps the local console up.

Opt-in: this leaves a web server with write access to your context files
running whenever you are logged in. It stays bound to loopback and keeps its
Host/Origin checks, but it is a standing door rather than one you open when
needed. contextd ui without this is the safer habit.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			if cfg.Mode == config.ModeServer {
				return fmt.Errorf("a server already serves its own console at /ui — this command is for solo and client spaces")
			}
			ok, err := confirmStandingConsole(cmd)
			if err != nil || !ok {
				return err
			}
			path, err := installUIService(addr)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✅ local console autostart installed → %s\n", path)
			fmt.Fprintf(cmd.OutOrStdout(), "It will be on %s after your next login (or start it now: %s)\n", addr, startHint())
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8790", "loopback address for the standing console")
	return cmd
}

func newUIUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the standing local console service",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, removed, err := uninstallUIService()
			if err != nil {
				return err
			}
			if !removed {
				fmt.Fprintf(cmd.OutOrStdout(), "no standing console installed (looked in %s)\n", path)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✅ removed (%s)\n", path)
			return nil
		},
	}
}

// anchorsFrom flattens the recorded project anchors for the graph, so the
// console can tell a live code reference from a dead one.
func anchorsFrom(cfg *config.Config) map[string]string {
	out := map[string]string{}
	for _, a := range cfg.Anchors {
		out[a.Project] = a.Path
	}
	return out
}
