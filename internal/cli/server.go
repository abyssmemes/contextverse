package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/server"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
	"github.com/abyssmemes/contextverse/internal/tui"
)

var flagServerDir string

func resolveServerDir() (string, error) {
	if flagServerDir != "" {
		return flagServerDir, nil
	}
	if v := os.Getenv("CONTEXTVERSE_SERVER_DIR"); v != "" {
		return v, nil
	}
	if config.ServerExists("/srv/contextverse") {
		return "/srv/contextverse", nil
	}
	return config.DefaultServerDataDir()
}

func newInitServerCmd() *cobra.Command {
	var (
		dataDir        string
		address        string
		port           int
		spaceName      string
		admin          string
		templateName   string
		nonInteractive bool
		force          bool
		noUI           bool
		openBrowser    bool
	)
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Initialize a ContextVerse server (UI by default; --noui for CLI)",
		Long: `Initialize a ContextVerse server.

By default this starts the setup web UI (and opens the browser).
For scripts/CI or headless installs, pass --noui (and typically --non-interactive).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				var err error
				dataDir, err = config.DefaultServerDataDir()
				if err != nil {
					return err
				}
			}
			if address == "" {
				address = config.DefaultListenAddr
			}
			if port == 0 {
				port = config.DefaultListenPort
			}

			// Default path: UI install
			if !noUI {
				if nonInteractive {
					return fmt.Errorf("UI setup cannot run with --non-interactive; use --noui for headless CLI install")
				}
				if config.ServerExists(dataDir) && !force {
					return fmt.Errorf("server already initialized at %s (use --force to wipe, or contextd server start)", dataDir)
				}
				if force {
					_ = os.RemoveAll(dataDir)
				}
				url := fmt.Sprintf("http://%s:%d/setup", loopbackHost(address), port)
				fmt.Fprintf(cmd.OutOrStdout(), "Starting setup UI at %s\n", url)
				fmt.Fprintf(cmd.OutOrStdout(), "Data dir: %s\n", dataDir)
				fmt.Fprintf(cmd.OutOrStdout(), "(CLI-only install: contextd init server --noui …)\n")
				srv := server.NewSetup(dataDir, address, port)
				if openBrowser {
					go func() {
						time.Sleep(300 * time.Millisecond)
						_ = execOpen(url)
					}()
				}
				pidPath := filepath.Join(dataDir, "pid")
				_ = os.MkdirAll(dataDir, 0o755)
				_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
				defer os.Remove(pidPath)
				return srv.ListenAndServe()
			}

			// --noui: classic CLI install
			if !nonInteractive {
				in := bufio.NewReader(cmd.InOrStdin())
				dataDir = prompt(in, "Data directory", dataDir)
				address = prompt(in, "Listen address", address)
				portStr := prompt(in, "Port", strconv.Itoa(port))
				port, _ = strconv.Atoi(portStr)
				spaceName = prompt(in, "Default space name", orDefault(spaceName, "team"))
				admin = prompt(in, "Admin username", orDefault(admin, "admin"))
				templateName = prompt(in, "Template", orDefault(templateName, "solo-default"))
			}
			if spaceName == "" {
				spaceName = "team"
			}
			if admin == "" {
				admin = "admin"
			}
			if templateName == "" {
				templateName = "solo-default"
			}
			if config.ServerExists(dataDir) && !force {
				return fmt.Errorf("server already initialized at %s (use --force)", dataDir)
			}
			if force {
				_ = os.RemoveAll(dataDir)
			}

			cfg := &config.ServerConfig{
				Mode:     config.ModeServer,
				DataDir:  dataDir,
				Listen:   config.ListenConfig{Address: address, Port: port},
				Backend:  config.Backend{Driver: "local"},
				Defaults: config.ServerDefaults{Space: spaceName},
			}
			if err := config.SaveServer(cfg); err != nil {
				return err
			}
			store, err := auth.OpenStore(dataDir)
			if err != nil {
				return err
			}
			if err := store.AddUser(admin, auth.RoleAdmin); err != nil {
				return err
			}
			token, _, err := store.CreateToken(admin, "init")
			if err != nil {
				return err
			}
			svc := &spacesvc.Service{DataDir: dataDir, Backend: cfg.Backend}
			if _, err := svc.Create(cmd.Context(), spaceName, templateName, true); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\n✅ Server initialized at %s (--noui)\n", dataDir)
			fmt.Fprintf(cmd.OutOrStdout(), "Listen: %s:%d\n", address, port)
			fmt.Fprintf(cmd.OutOrStdout(), "Space:  %s\n", spaceName)
			fmt.Fprintf(cmd.OutOrStdout(), "Admin:  %s\n\n", admin)
			fmt.Fprintf(cmd.OutOrStdout(), "Save this token (shown once):\n  %s\n\n", token)
			fmt.Fprintf(cmd.OutOrStdout(), "Next:\n  contextd server start --server-dir %s\n", dataDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "server data directory (default: ~/.contextverse-server)")
	cmd.Flags().StringVar(&address, "address", config.DefaultListenAddr, "listen address")
	cmd.Flags().IntVar(&port, "port", config.DefaultListenPort, "listen port")
	cmd.Flags().StringVar(&spaceName, "space", "team", "default space name (with --noui)")
	cmd.Flags().StringVar(&admin, "admin", "admin", "admin username (with --noui)")
	cmd.Flags().StringVar(&templateName, "template", "solo-default", "template to seed (with --noui)")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "do not prompt (requires --noui)")
	cmd.Flags().BoolVar(&force, "force", false, "wipe and recreate data dir")
	cmd.Flags().BoolVar(&noUI, "noui", false, "CLI-only install (no setup web UI)")
	cmd.Flags().BoolVar(&openBrowser, "open", true, "open the setup UI in a browser (ignored with --noui)")
	return cmd
}

func loopbackHost(address string) string {
	if address == "0.0.0.0" || address == "::" || address == "" {
		return "127.0.0.1"
	}
	return address
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run and manage the ContextVerse server",
	}
	cmd.AddCommand(newServerStartCmd())
	cmd.AddCommand(newServerStopCmd())
	cmd.AddCommand(newServerStatusCmd())
	cmd.AddCommand(newServerHealthCmd())
	cmd.AddCommand(newServerLogsCmd())
	cmd.AddCommand(newServerUnitCmd())
	return cmd
}

func newServerStartCmd() *cobra.Command {
	var (
		daemon  bool
		address string
		port    int
		openUI  bool
	)
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the HTTP server (opens setup UI if not initialized)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			var srv *server.Server
			if !config.ServerExists(dir) {
				if address == "" {
					address = config.DefaultListenAddr
				}
				if port == 0 {
					port = config.DefaultListenPort
				}
				logx.L().Info("no server config — starting setup UI", "data_dir", dir, "url", fmt.Sprintf("http://%s:%d/setup", address, port))
				srv = server.NewSetup(dir, address, port)
			} else {
				cfg, err := config.LoadServer(dir)
				if err != nil {
					return err
				}
				store, err := auth.OpenStore(dir)
				if err != nil {
					return err
				}
				srv = server.New(cfg, store)
			}
			if daemon {
				return fmt.Errorf("daemon mode not yet implemented — run in foreground (or use systemd/launchd)")
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			pidPath := filepath.Join(dir, "pid")
			_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
			defer os.Remove(pidPath)
			listenAddr := address
			listenPort := port
			if config.ServerExists(dir) {
				if cfg, err := config.LoadServer(dir); err == nil {
					listenAddr = cfg.Listen.Address
					listenPort = cfg.Listen.Port
				}
			}
			if err := checkListenFree(listenAddr, listenPort); err != nil {
				return err
			}
			if openUI {
				url := srv.Cfg.BaseURL()
				if !config.ServerExists(dir) {
					url = fmt.Sprintf("http://%s:%d/setup", loopbackHost(address), port)
				}
				go func() {
					time.Sleep(300 * time.Millisecond)
					_ = execOpen(url)
				}()
			}

			wishCtx, wishCancel := context.WithCancel(context.Background())
			defer wishCancel()
			if config.ServerExists(dir) {
				if cfg, err := config.LoadServer(dir); err == nil && cfg.TUI.SSH.Enabled {
					listen := cfg.TUI.SSH.Listen
					if listen == "" {
						listen = config.DefaultTUISSHListen
					}
					go func() {
						logx.L().Info("tui ssh (Wish) listening", "addr", listen)
						if err := tui.ListenAndServeSSH(wishCtx, tui.SSHOptions{DataDir: dir, Listen: listen}); err != nil && wishCtx.Err() == nil {
							logx.L().Error("tui ssh stopped", "err", err)
						}
					}()
				}
			}

			return runServerUntilSignal(srv, wishCancel)
		},
	}
	cmd.Flags().BoolVar(&daemon, "daemon", false, "run as daemon (not yet implemented — use systemd/launchd)")
	cmd.Flags().StringVar(&address, "address", config.DefaultListenAddr, "listen address when running setup UI")
	cmd.Flags().IntVar(&port, "port", config.DefaultListenPort, "listen port when running setup UI")
	cmd.Flags().BoolVar(&openUI, "open", true, "open setup/dashboard in a browser (default on)")
	return cmd
}

// runServerUntilSignal serves until SIGINT/SIGTERM, then graceful Shutdown.
func runServerUntilSignal(srv *server.Server, onStop func()) error {
	ignoreTerminalStopSignals()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case sig := <-sigCh:
		logx.L().Info("shutdown signal", "signal", sig.String())
		if onStop != nil {
			onStop()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logx.L().Error("graceful shutdown failed", "err", err)
			return err
		}
		err := <-errCh
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			logx.L().Info("server stopped")
			return nil
		}
		return err
	}
}

func execOpen(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		fmt.Fprintf(os.Stderr, "open: %s\n", url)
		return nil
	}
}

func checkListenFree(address string, port int) error {
	if port <= 0 {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", address, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		hint := fmt.Sprintf("port %d is busy", port)
		if raw, rerr := os.ReadFile(filepath.Join(mustServerDir(), "pid")); rerr == nil {
			hint += fmt.Sprintf(" (pid file says %s — try: contextd server stop)", strings.TrimSpace(string(raw)))
		} else {
			hint += " — another contextd may still be listening; try: contextd server stop, or: lsof -iTCP:" + strconv.Itoa(port) + " -sTCP:LISTEN"
		}
		return fmt.Errorf("cannot bind %s: %w\n%s", addr, err, hint)
	}
	_ = ln.Close()
	return nil
}

func mustServerDir() string {
	dir, err := resolveServerDir()
	if err != nil {
		return ""
	}
	return dir
}

func newServerStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop a server started under this data dir (via pid file)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(filepath.Join(dir, "pid"))
			if err != nil {
				return fmt.Errorf("no pid file — is the server running under this data dir?\nTry: lsof -iTCP:8743 -sTCP:LISTEN")
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				return err
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				// Suspended / already exiting — last resort.
				if err2 := proc.Kill(); err2 != nil {
					return fmt.Errorf("signal pid %d: %v (kill: %w)", pid, err, err2)
				}
			}
			_ = os.Remove(filepath.Join(dir, "pid"))
			fmt.Fprintf(cmd.OutOrStdout(), "stopped pid %d\n", pid)
			return nil
		},
	}
}

func newServerStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show server config and process status",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if !config.ServerExists(dir) {
				return fmt.Errorf("no server config at %s — run contextd init server", dir)
			}
			cfg, err := config.LoadServer(dir)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "data_dir: %s\n", dir)
			fmt.Fprintf(cmd.OutOrStdout(), "listen:   %s\n", cfg.Addr())
			fmt.Fprintf(cmd.OutOrStdout(), "backend:  %s\n", cfg.Backend.Driver)
			fmt.Fprintf(cmd.OutOrStdout(), "space:    %s\n", cfg.Defaults.Space)
			pidPath := filepath.Join(dir, "pid")
			if raw, err := os.ReadFile(pidPath); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "pid:      %s", string(raw))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "pid:      (not running / no pid file)\n")
			}
			return nil
		},
	}
}

func newServerHealthCmd() *cobra.Command {
	var url string
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Probe GET /health",
		RunE: func(cmd *cobra.Command, args []string) error {
			if url == "" {
				dir, err := resolveServerDir()
				if err != nil {
					return err
				}
				cfg, err := config.LoadServer(dir)
				if err != nil {
					return err
				}
				url = cfg.BaseURL() + "/health"
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				return fmt.Errorf("health status %d", res.StatusCode)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "health URL override")
	return cmd
}

func newServerLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Show server log file (if configured)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			path := filepath.Join(dir, "logs", "server.log")
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("no log file at %s (foreground mode logs to stderr)", path)
			}
			_, _ = cmd.OutOrStdout().Write(raw)
			return nil
		},
	}
}

func newServerUnitCmd() *cobra.Command {
	var (
		outPath string
		binPath string
		user     string
	)
	cmd := &cobra.Command{
		Use:   "unit",
		Short: "Print a systemd unit for this server (install under /etc/systemd/system/)",
		Long: `Writes a Type=simple unit with Restart=always, SIGTERM graceful stop,
and --open=false. Pipe to a file or use --out.

  contextd server unit --server-dir /srv/contextverse | sudo tee /etc/systemd/system/contextd.service
  sudo systemctl daemon-reload && sudo systemctl enable --now contextd

Upgrade one node: install new binary, then systemctl restart contextd.
Fleet: drain from LB → restart → wait for GET /health 200 → undrain.
Never use kill -9 for upgrades.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if binPath == "" {
				binPath, err = os.Executable()
				if err != nil {
					binPath = "contextd"
				}
			}
			if user == "" {
				user = "contextd"
			}
			unit := renderSystemdUnit(binPath, dir, user)
			if outPath == "" {
				_, err = fmt.Fprint(cmd.OutOrStdout(), unit)
				return err
			}
			if err := os.WriteFile(outPath, []byte(unit), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "write unit file to path instead of stdout")
	cmd.Flags().StringVar(&binPath, "bin", "", "contextd binary path (default: this executable)")
	cmd.Flags().StringVar(&user, "user", "contextd", "User=/Group= in the unit")
	return cmd
}

func renderSystemdUnit(bin, dataDir, user string) string {
	return fmt.Sprintf(`[Unit]
Description=ContextVerse server (contextd)
Documentation=https://github.com/abyssmemes/contextverse
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s server start --server-dir %s --open=false
Restart=always
RestartSec=3
TimeoutStopSec=30
KillSignal=SIGTERM
KillMode=mixed
LimitNOFILE=65536

# Hardening (relax ReadWritePaths if your data dir differs)
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=%s

# Readiness for LB / orchestrators: GET http://<listen>/health → 200 {"status":"ok"}

[Install]
WantedBy=multi-user.target
`, user, user, bin, dataDir, dataDir)
}

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage server users"}

	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, _ := cmd.Flags().GetString("role")
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			if err := store.AddUser(args[0], auth.Role(role)); err != nil {
				return err
			}
			token, _, err := store.CreateToken(args[0], "created")
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "user %s added (role=%s)\ntoken: %s\n", args[0], role, token)
			return nil
		},
	}
	add.Flags().String("role", "contributor", "role/policy preset: admin|space-lead|contributor|viewer")
	cmd.AddCommand(add)

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List users",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			users, err := store.ListUsers()
			if err != nil {
				return err
			}
			for _, u := range users {
				pols := strings.Join(u.EffectivePolicies(), ",")
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", u.Name, u.Role, pols)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "role <name> <role>",
		Short: "Set user role (also sets policies to that preset)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			return store.SetRole(args[0], auth.Role(args[1]))
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "policy <name> <policy>[,policy...]",
		Short: "Set user policies (comma-separated)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			pols := strings.Split(args[1], ",")
			for i := range pols {
				pols[i] = strings.TrimSpace(pols[i])
			}
			return store.SetPolicies(args[0], pols)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a user and revoke tokens",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			return store.RemoveUser(args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "reset-token <name>",
		Short: "Issue a new token for a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			token, _, err := store.CreateToken(args[0], "reset")
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", token)
			return nil
		},
	})
	return cmd
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Auth token management (server host)"}
	tok := &cobra.Command{Use: "token", Short: "Manage API tokens"}

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a token for a user",
		RunE: func(cmd *cobra.Command, args []string) error {
			user, _ := cmd.Flags().GetString("user")
			label, _ := cmd.Flags().GetString("label")
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			token, rec, err := store.CreateToken(user, label)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "id: %s\ntoken: %s\n", rec.ID, token)
			return nil
		},
	}
	create.Flags().String("user", "", "username")
	create.Flags().String("label", "", "token label")
	_ = create.MarkFlagRequired("user")
	tok.AddCommand(create)

	list := &cobra.Command{
		Use:   "list",
		Short: "List tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			user, _ := cmd.Flags().GetString("user")
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			tokens, err := store.ListTokens(user)
			if err != nil {
				return err
			}
			for _, t := range tokens {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", t.ID, t.User, t.Role, t.Label)
			}
			return nil
		},
	}
	list.Flags().String("user", "", "filter by user")
	tok.AddCommand(list)

	tok.AddCommand(&cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke a token by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			return store.RevokeToken(args[0])
		},
	})
	cmd.AddCommand(tok)

	login := &cobra.Command{
		Use:   "login",
		Short: "Login with username/password (userpass) — local --server-dir or --server URL",
		RunE: func(cmd *cobra.Command, args []string) error {
			user, _ := cmd.Flags().GetString("user")
			pass, _ := cmd.Flags().GetString("password")
			serverURL, _ := cmd.Flags().GetString("server")
			if user == "" {
				return fmt.Errorf("--user required")
			}
			if pass == "" {
				fmt.Fprint(cmd.ErrOrStderr(), "Password: ")
				var line string
				_, _ = fmt.Fscanln(cmd.InOrStdin(), &line)
				pass = strings.TrimSpace(line)
			}
			if serverURL != "" {
				tok, err := remoteUserpassLogin(strings.TrimRight(serverURL, "/"), user, pass)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), tok)
				return nil
			}
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			tok, _, err := store.LoginUserpass(user, pass)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), tok)
			return nil
		},
	}
	login.Flags().String("user", "", "username")
	login.Flags().String("password", "", "password (prompt if empty)")
	login.Flags().String("server", "", "remote server base URL (e.g. http://127.0.0.1:8743)")
	cmd.AddCommand(login)

	pw := &cobra.Command{
		Use:   "password-set <user>",
		Short: "Set a user's password for userpass login",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pass, _ := cmd.Flags().GetString("password")
			if pass == "" {
				return fmt.Errorf("--password required")
			}
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			if err := store.SetPassword(args[0], pass); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "password set for %s\n", args[0])
			return nil
		},
	}
	pw.Flags().String("password", "", "new password")
	cmd.AddCommand(pw)
	return cmd
}
