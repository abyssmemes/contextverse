package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	bm "github.com/charmbracelet/wish/bubbletea"

	"github.com/abyssmemes/contextverse/internal/config"
)

// SSHOptions configures the Model B Wish listener.
type SSHOptions struct {
	DataDir string
	Listen  string
}

// ListenAndServeSSH serves the server admin TUI over Wish (blocking).
// Auth: OpenSSH authorized_keys at <dataDir>/auth/tui_authorized_keys.
// Host key: <dataDir>/auth/tui_host_ed25519 (created if missing).
func ListenAndServeSSH(ctx context.Context, opts SSHOptions) error {
	if opts.DataDir == "" {
		return fmt.Errorf("tui ssh: data dir required")
	}
	if opts.Listen == "" {
		opts.Listen = config.DefaultTUISSHListen
	}
	authKeys := config.TUISSHAuthorizedKeysPath(opts.DataDir)
	hostKey := config.TUISSHHostKeyPath(opts.DataDir)
	if err := os.MkdirAll(filepath.Dir(hostKey), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(authKeys); err != nil {
		return fmt.Errorf("tui ssh: no authorized keys at %s — add one with: contextd tui ssh add-key <pubkey>", authKeys)
	}

	dataDir := opts.DataDir
	s, err := wish.NewServer(
		wish.WithAddress(opts.Listen),
		wish.WithHostKeyPath(hostKey),
		wish.WithAuthorizedKeys(authKeys),
		wish.WithMiddleware(
			bm.Middleware(func(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
				return newServerModel(dataDir), []tea.ProgramOption{tea.WithAltScreen()}
			}),
			activeterm.Middleware(),
		),
	)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)
		<-errCh
		return ctx.Err()
	case err := <-errCh:
		if err == nil || errors.Is(err, ssh.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// AppendAuthorizedKey appends an OpenSSH public key line to the TUI allowlist.
func AppendAuthorizedKey(dataDir, pubKeyLine string) error {
	pubKeyLine = trimKeyLine(pubKeyLine)
	if pubKeyLine == "" {
		return fmt.Errorf("empty public key")
	}
	path := config.TUISSHAuthorizedKeysPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, pubKeyLine)
	return err
}

func trimKeyLine(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 {
		last := s[len(s)-1]
		if last == ' ' || last == '\t' || last == '\n' || last == '\r' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}
