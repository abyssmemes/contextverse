package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
)

// Autostart for the client sync daemon.
//
// `deploy/contextd.service` and `contextd.plist` supervise the *server*, so
// until now a client daemon died with the terminal that started it and had to
// be restarted by hand after every reboot — which is the one thing background
// sync is supposed to spare you.
//
// These units are deliberately **per-user**, not system-wide: the daemon syncs
// one person's space using that person's token, and installing it as root would
// run it as the wrong user against a credential it should not be able to read.
// That is why this needs no sudo on either platform.

const launchdLabel = "dev.contextverse.daemon"

func newDaemonInstallCmd() *cobra.Command {
	var start bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install a per-user service so the daemon starts at login",
		Long: `Register the sync daemon with your login session manager.

launchd (macOS) or systemd --user (Linux), installed for your user only — the
daemon reads your token and syncs your space, so it must not run as root.

Windows is not supported here: use Task Scheduler with
"contextd daemon run --dir <space>".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			if cfg.Mode != config.ModeClient {
				return fmt.Errorf("autostart is for client mode (got %s): only a client syncs from a server", cfg.Mode)
			}
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			abs, err := filepath.Abs(root)
			if err != nil {
				return err
			}

			path, err := installService(bin, abs, daemonInterval(cfg))
			if err != nil {
				return err
			}
			logx.L().Info("daemon service installed", "path", path, "space", abs)
			fmt.Fprintf(cmd.OutOrStdout(), "✅ autostart installed → %s\n", path)

			if start {
				if err := startService(); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "installed, but starting it failed: %v\n", err)
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "✅ started")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "It will start at your next login, or now with: %s\n", startHint())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&start, "start", true, "start the service immediately as well")
	return cmd
}

func newDaemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the per-user autostart service",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, removed, err := uninstallService()
			if err != nil {
				return err
			}
			if !removed {
				fmt.Fprintf(cmd.OutOrStdout(), "no autostart service installed (looked in %s)\n", path)
				return nil
			}
			logx.L().Info("daemon service removed", "path", path)
			fmt.Fprintf(cmd.OutOrStdout(), "✅ autostart removed (%s)\n", path)
			fmt.Fprintln(cmd.OutOrStdout(), "A daemon started by hand keeps running — stop it with: contextd daemon stop")
			return nil
		},
	}
}

// servicePath is where the per-user unit lives on this platform.
func servicePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", "contextd-daemon.service"), nil
	default:
		return "", fmt.Errorf("autostart is not supported on %s; schedule `contextd daemon run --dir <space>` with your OS scheduler", runtime.GOOS)
	}
}

func serviceInstalled() bool {
	path, err := servicePath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func installService(bin, spaceRoot string, interval int) (string, error) {
	path, err := servicePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	var body string
	switch runtime.GOOS {
	case "darwin":
		body = launchdPlist(bin, spaceRoot)
	case "linux":
		body = systemdUserUnit(bin, spaceRoot, interval)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		// Without this the unit file exists but systemd has not read it.
		if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			logx.L().Warn("systemctl --user daemon-reload", "err", err, "out", strings.TrimSpace(string(out)))
		}
	}
	return path, nil
}

func uninstallService() (string, bool, error) {
	path, err := servicePath()
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path, false, nil
	}
	_ = stopService()
	if err := os.Remove(path); err != nil {
		return path, false, err
	}
	if runtime.GOOS == "linux" {
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	return path, true, nil
}

func startService() error {
	path, err := servicePath()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl load: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "linux":
		if out, err := exec.Command("systemctl", "--user", "enable", "--now", "contextd-daemon.service").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user enable --now: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return fmt.Errorf("unsupported platform %s", runtime.GOOS)
}

func stopService() error {
	path, err := servicePath()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("launchctl", "unload", "-w", path).Run()
	case "linux":
		return exec.Command("systemctl", "--user", "disable", "--now", "contextd-daemon.service").Run()
	}
	return nil
}

func startHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "launchctl load -w ~/Library/LaunchAgents/" + launchdLabel + ".plist"
	case "linux":
		return "systemctl --user enable --now contextd-daemon.service"
	default:
		return "contextd daemon start"
	}
}

func launchdPlist(bin, spaceRoot string) string {
	// KeepAlive restarts the loop if it exits unexpectedly; the loop handles its
	// own polling interval, so launchd must not also throttle it with
	// StartInterval — that would double-schedule the work.
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
		<string>run</string>
		<string>--dir</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, launchdLabel, bin, spaceRoot, daemonLogPath(spaceRoot), daemonLogPath(spaceRoot))
}

func systemdUserUnit(bin, spaceRoot string, interval int) string {
	return fmt.Sprintf(`[Unit]
Description=ContextVerse client sync daemon
Documentation=https://abyssmemes.github.io/contextverse/
After=network-online.target

[Service]
Type=simple
ExecStart=%s daemon run --dir %s
Restart=always
RestartSec=10
# The loop paces itself and backs off on failure, so systemd only needs to keep
# the process alive — no timer, no extra scheduling.
Environment=CONTEXTVERSE_DAEMON_INTERVAL=%d
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
`, bin, spaceRoot, interval)
}

// newDaemonUnitCmd prints the unit without installing it, for people who manage
// their machines from configuration rather than from a CLI.
func newDaemonUnitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unit",
		Short: "Print the autostart unit for this platform (does not install)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			abs, _ := filepath.Abs(root)
			var body string
			switch runtime.GOOS {
			case "darwin":
				body = launchdPlist(bin, abs)
			case "linux":
				body = systemdUserUnit(bin, abs, daemonInterval(cfg))
			default:
				return fmt.Errorf("no unit template for %s", runtime.GOOS)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), body)
			return err
		},
	}
}

// serviceSupported reports whether this platform has an autostart template.
// Callers use it to avoid offering an option the next step cannot honour.
func serviceSupported() bool {
	switch runtime.GOOS {
	case "darwin", "linux":
		return true
	default:
		return false
	}
}
