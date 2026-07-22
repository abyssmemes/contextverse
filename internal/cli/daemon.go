package cli

import (
	"context"
	"fmt"
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
	}
	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonStatusCmd())
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

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon pid / running state",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "space:    %s\n", root)
			fmt.Fprintf(cmd.OutOrStdout(), "mode:     %s\n", cfg.Mode)
			fmt.Fprintf(cmd.OutOrStdout(), "interval: %ds\n", daemonInterval(cfg))
			raw, err := os.ReadFile(daemonPidPath(root))
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "daemon:   not running")
				return nil
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
			alive := false
			if pid > 0 {
				if proc, err := os.FindProcess(pid); err == nil {
					alive = proc.Signal(syscall.Signal(0)) == nil
				}
			}
			if alive {
				fmt.Fprintf(cmd.OutOrStdout(), "daemon:   running pid=%d\n", pid)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "daemon:   stale pid file (%d)\n", pid)
			}
			return nil
		},
	}
}

func daemonInterval(cfg *config.Config) int {
	if cfg.Daemon.IntervalSec > 0 {
		return cfg.Daemon.IntervalSec
	}
	return 60
}

func runDaemonLoop(ctx context.Context, root string, cfg *config.Config) error {
	interval := time.Duration(daemonInterval(cfg)) * time.Second
	logx.L().Info("daemon loop start", "interval", interval.String(), "space", root)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	poll := func() {
		pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		pulled, err := syncclient.PollOnce(pctx, root, cfg)
		if err != nil {
			logx.L().Warn("daemon poll", "err", err)
			return
		}
		if !pulled {
			return
		}
	}

	poll() // immediate
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sigCh:
			logx.L().Info("daemon stopping")
			return nil
		case <-ticker.C:
			poll()
		}
	}
}
