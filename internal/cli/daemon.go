package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/syncclient"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Background client sync (poll server head → pull)",
		Long: `Keep this machine's copy of the space current, in the background.

The daemon polls the server's head and pulls when it moves. It is deliberately
one-way: it never pushes your local edits. Publishing stays an explicit
contextd push, so nothing you were in the middle of writing leaves the machine
because a timer fired.

Failures widen the polling interval up to 15 minutes and the first success
restores it, so an unreachable server does not mean a failed request every
minute forever.

  contextd daemon install    start it at login (per-user service)
  contextd daemon start      start it now, for this session
  contextd daemon status     state, interval, last sync
  contextd daemon logs       what it has been doing`,
	}
	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonStatusCmd())
	cmd.AddCommand(newDaemonLogsCmd())
	cmd.AddCommand(newDaemonInstallCmd())
	cmd.AddCommand(newDaemonUninstallCmd())
	cmd.AddCommand(newDaemonUnitCmd())
	cmd.AddCommand(newDaemonRunCmd())
	return cmd
}

func daemonPidPath(root string) string {
	return filepath.Join(root, ".sync", "daemon.pid")
}

func daemonLogPath(root string) string {
	return filepath.Join(root, ".sync", "daemon.log")
}

func newDaemonStartCmd() *cobra.Command {
	var foreground bool
	var interval int
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the client sync daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			if cfg.Mode != config.ModeClient {
				return fmt.Errorf("daemon requires client mode (got %s)", cfg.Mode)
			}
			if interval > 0 {
				cfg.Daemon.IntervalSec = interval
				_ = config.Save(cfg)
			}
			if foreground {
				return runDaemonLoop(cmd.Context(), root, cfg)
			}
			if raw, err := os.ReadFile(daemonPidPath(root)); err == nil {
				if pid, _ := strconv.Atoi(strings.TrimSpace(string(raw))); pid > 0 {
					if proc, err := os.FindProcess(pid); err == nil {
						if err := proc.Signal(syscall.Signal(0)); err == nil {
							return fmt.Errorf("daemon already running (pid %d); stop it first", pid)
						}
					}
				}
			}
			_ = os.MkdirAll(filepath.Join(root, ".sync"), 0o755)
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			logf, err := os.OpenFile(daemonLogPath(root), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			c := exec.Command(bin, "daemon", "run", "--dir", root)
			c.Stdout = logf
			c.Stderr = logf
			c.Stdin = nil
			if err := c.Start(); err != nil {
				_ = logf.Close()
				return err
			}
			_ = logf.Close()
			if err := os.WriteFile(daemonPidPath(root), []byte(strconv.Itoa(c.Process.Pid)+"\n"), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "daemon started pid=%d interval=%ds log=%s\n",
				c.Process.Pid, daemonInterval(cfg), daemonLogPath(root))
			return nil
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in this terminal (no detach)")
	cmd.Flags().IntVar(&interval, "interval", 0, "poll interval seconds (default 60; persists to config)")
	return cmd
}

func newDaemonRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "run",
		Short:  "Foreground poll loop (used by daemon start)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			if cfg.Mode != config.ModeClient {
				return fmt.Errorf("daemon requires client mode")
			}
			_ = os.WriteFile(daemonPidPath(root), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
			defer func() { _ = os.Remove(daemonPidPath(root)) }()
			return runDaemonLoop(cmd.Context(), root, cfg)
		},
	}
	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the client sync daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(daemonPidPath(root))
			if err != nil {
				return fmt.Errorf("no daemon pid file at %s", daemonPidPath(root))
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil || pid <= 0 {
				return fmt.Errorf("invalid pid file")
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				_ = os.Remove(daemonPidPath(root))
				return fmt.Errorf("signal pid %d: %w", pid, err)
			}
			_ = os.Remove(daemonPidPath(root))
			fmt.Fprintf(cmd.OutOrStdout(), "stopped daemon pid=%d\n", pid)
			return nil
		},
	}
}

// DaemonStatus is the structured form of `daemon status`.
type DaemonStatus struct {
	Space       string `json:"space" yaml:"space"`
	Mode        string `json:"mode" yaml:"mode"`
	IntervalSec int    `json:"interval_sec" yaml:"interval_sec"`
	State       string `json:"state" yaml:"state"` // running | stopped | stale
	PID         int    `json:"pid,omitempty" yaml:"pid,omitempty"`
	LastHead    string `json:"last_head,omitempty" yaml:"last_head,omitempty"`
	LastSyncAt  string `json:"last_sync_at,omitempty" yaml:"last_sync_at,omitempty"`
	LogPath     string `json:"log_path" yaml:"log_path"`
	Managed     bool   `json:"managed" yaml:"managed"` // supervised by launchd/systemd
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon state, interval and last sync",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			st := DaemonStatus{
				Space:       root,
				Mode:        string(cfg.Mode),
				IntervalSec: daemonInterval(cfg),
				State:       "stopped",
				LastHead:    cfg.Sync.LastHead,
				LogPath:     daemonLogPath(root),
				Managed:     serviceInstalled(),
			}
			if !cfg.Sync.LastSyncAt.IsZero() {
				st.LastSyncAt = cfg.Sync.LastSyncAt.Format(time.RFC3339)
			}
			if raw, err := os.ReadFile(daemonPidPath(root)); err == nil {
				pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
				st.PID = pid
				st.State = "stale"
				if pid > 0 {
					if proc, err := os.FindProcess(pid); err == nil && proc.Signal(syscall.Signal(0)) == nil {
						st.State = "running"
					}
				}
			}

			return emit(cmd.OutOrStdout(), st, func(w io.Writer) error {
				fmt.Fprintf(w, "space:     %s\n", st.Space)
				fmt.Fprintf(w, "mode:      %s\n", st.Mode)
				fmt.Fprintf(w, "interval:  %ds\n", st.IntervalSec)
				switch st.State {
				case "running":
					fmt.Fprintf(w, "daemon:    running pid=%d\n", st.PID)
				case "stale":
					fmt.Fprintf(w, "daemon:    stale pid file (%d) — run contextd daemon start\n", st.PID)
				default:
					fmt.Fprintf(w, "daemon:    not running\n")
				}
				if st.Managed {
					fmt.Fprintf(w, "autostart: installed (starts at login)\n")
				} else {
					fmt.Fprintf(w, "autostart: not installed — contextd daemon install\n")
				}
				if st.LastSyncAt != "" {
					fmt.Fprintf(w, "last sync: %s\n", st.LastSyncAt)
				}
				if st.LastHead != "" {
					fmt.Fprintf(w, "head:      %s\n", st.LastHead)
				}
				fmt.Fprintf(w, "log:       %s\n", st.LogPath)
				return nil
			})
		},
	}
}

func newDaemonLogsCmd() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the daemon log",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			path := daemonLogPath(root)
			raw, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no daemon log at %s — the daemon has not run yet", path)
				}
				return err
			}
			all := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			if lines > 0 && len(all) > lines {
				all = all[len(all)-lines:]
			}
			for _, l := range all {
				fmt.Fprintln(cmd.OutOrStdout(), l)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 200, "show only the last N lines (0 for all)")
	return cmd
}

func daemonInterval(cfg *config.Config) int {
	if cfg.Daemon.IntervalSec > 0 {
		return cfg.Daemon.IntervalSec
	}
	return 60
}

// daemonMaxBackoff caps how far a failing poll backs off. A laptop that wakes
// to a server which came back an hour ago should notice within minutes, not
// hours, so the cap stays well under a working day.
const daemonMaxBackoff = 15 * time.Minute

func runDaemonLoop(ctx context.Context, root string, cfg *config.Config) error {
	base := time.Duration(daemonInterval(cfg)) * time.Second
	logx.L().Info("daemon loop start", "interval", base.String(), "space", root)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// A fixed ticker meant an unreachable server produced an identical failed
	// request every interval, forever — noise in the log, load on a server that
	// is probably already unwell, and a flat battery on a laptop off the VPN.
	// Failures widen the gap; the first success restores it.
	delay := base
	failures := 0

	timer := time.NewTimer(0) // fire immediately, then reschedule per outcome
	defer timer.Stop()

	poll := func() {
		pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		_, err := syncclient.PollOnce(pctx, root, cfg)
		if err != nil {
			failures++
			delay = backoffFor(base, failures)
			logx.L().Warn("daemon poll", "err", err, "failures", failures, "next_in", delay.String())
			return
		}
		if failures > 0 {
			logx.L().Info("daemon poll recovered", "after_failures", failures)
		}
		failures = 0
		delay = base
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sigCh:
			logx.L().Info("daemon stopping")
			return nil
		case <-timer.C:
			poll()
			timer.Reset(delay)
		}
	}
}

// backoffFor doubles the interval per consecutive failure up to the cap. The
// first failure still retries at the normal interval — a single dropped packet
// should not push the next attempt out to minutes.
func backoffFor(base time.Duration, failures int) time.Duration {
	if failures <= 1 {
		return base
	}
	d := base
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= daemonMaxBackoff {
			return daemonMaxBackoff
		}
	}
	return d
}
