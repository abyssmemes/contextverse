package cli

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const (
	tuiLoginBegin = "# >>> contextverse tui login >>>"
	tuiLoginEnd   = "# <<< contextverse tui login <<<"
)

func newTUILoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Model A: wire TUI as SSH login program (host account profile)",
		Long: `Model A — opt-in per Unix user. On interactive SSH login, launch the TUI;
when you quit (q), you land in that user's normal shell.

Does not touch host sshd ForceCommand by default (writes a marked block into
the user's login profile). Prints a ForceCommand snippet for operators who
prefer sshd Match User.

Not the same as Model B (contextd tui ssh / Wish).`,
	}
	cmd.AddCommand(newTUILoginEnableCmd())
	cmd.AddCommand(newTUILoginDisableCmd())
	cmd.AddCommand(newTUILoginStatusCmd())
	return cmd
}

func newTUILoginEnableCmd() *cobra.Command {
	var (
		userName string
		asServer bool
		client   bool
	)
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Install TUI auto-launch into the user's login profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			u, home, err := resolveLoginUser(userName)
			if err != nil {
				return err
			}
			if asServer && client {
				return fmt.Errorf("use only one of --server or --client")
			}
			mode := "client"
			if asServer {
				mode = "server"
			}
			block := tuiLoginBlock(mode)
			profile := loginProfilePath(home)
			if err := upsertMarkedBlock(profile, tuiLoginBegin, tuiLoginEnd, block); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Model A login enabled for %s\n", u)
			fmt.Fprintf(cmd.OutOrStdout(), "Profile: %s\n", profile)
			fmt.Fprintf(cmd.OutOrStdout(), "Mode:    %s TUI on interactive SSH; q → shell\n", mode)
			fmt.Fprintln(cmd.OutOrStdout(), "")
			fmt.Fprintln(cmd.OutOrStdout(), "Optional sshd ForceCommand (no shell unless they bypass):")
			bin, _ := os.Executable()
			if bin == "" {
				bin = "contextd"
			}
			if mode == "server" {
				fmt.Fprintf(cmd.OutOrStdout(), "  ForceCommand %s tui --server\n", bin)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  ForceCommand %s tui\n", bin)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&userName, "user", "", "Unix username (default: current user)")
	cmd.Flags().BoolVar(&asServer, "server", false, "launch server admin TUI")
	cmd.Flags().BoolVar(&client, "client", false, "launch client/solo TUI (default if not --server)")
	return cmd
}

func newTUILoginDisableCmd() *cobra.Command {
	var userName string
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Remove TUI auto-launch from the user's login profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			u, home, err := resolveLoginUser(userName)
			if err != nil {
				return err
			}
			profile := loginProfilePath(home)
			removed, err := removeMarkedBlock(profile, tuiLoginBegin, tuiLoginEnd)
			if err != nil {
				return err
			}
			if !removed {
				fmt.Fprintf(cmd.OutOrStdout(), "no Model A block in %s (user %s)\n", profile, u)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Model A login disabled for %s (%s)\n", u, profile)
			return nil
		},
	}
	cmd.Flags().StringVar(&userName, "user", "", "Unix username (default: current user)")
	return cmd
}

func newTUILoginStatusCmd() *cobra.Command {
	var userName string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether Model A login block is installed",
		RunE: func(cmd *cobra.Command, args []string) error {
			u, home, err := resolveLoginUser(userName)
			if err != nil {
				return err
			}
			profile := loginProfilePath(home)
			raw, err := os.ReadFile(profile)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintf(cmd.OutOrStdout(), "user=%s profile=%s enabled=false (no profile)\n", u, profile)
					return nil
				}
				return err
			}
			enabled := strings.Contains(string(raw), tuiLoginBegin)
			fmt.Fprintf(cmd.OutOrStdout(), "user=%s profile=%s enabled=%v\n", u, profile, enabled)
			return nil
		},
	}
	cmd.Flags().StringVar(&userName, "user", "", "Unix username (default: current user)")
	return cmd
}

func resolveLoginUser(name string) (uname, home string, err error) {
	var u *user.User
	if name == "" {
		u, err = user.Current()
	} else {
		u, err = user.Lookup(name)
	}
	if err != nil {
		return "", "", err
	}
	return u.Username, u.HomeDir, nil
}

func loginProfilePath(home string) string {
	// Prefer zsh on macOS; bash elsewhere; always one file we control.
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".zprofile")
	}
	if _, err := os.Stat(filepath.Join(home, ".bash_profile")); err == nil {
		return filepath.Join(home, ".bash_profile")
	}
	return filepath.Join(home, ".profile")
}

func tuiLoginBlock(mode string) string {
	bin, err := os.Executable()
	if err != nil {
		bin = "contextd"
	}
	args := "tui"
	if mode == "server" {
		args = "tui --server"
	}
	var b strings.Builder
	b.WriteString(tuiLoginBegin + "\n")
	b.WriteString("# Managed by: contextd tui login enable|disable\n")
	b.WriteString("# Skip once: CONTEXTVERSE_TUI_SKIP=1 ssh …\n")
	b.WriteString("if [ -n \"${SSH_CONNECTION:-}\" ] && [ -t 0 ] && [ -z \"${CONTEXTVERSE_TUI_SKIP:-}\" ]; then\n")
	b.WriteString("  export CONTEXTVERSE_MODEL_A=1\n")
	b.WriteString(fmt.Sprintf("  \"%s\" %s || true\n", bin, args))
	b.WriteString("fi\n")
	b.WriteString(tuiLoginEnd + "\n")
	return b.String()
}

func upsertMarkedBlock(path, begin, end, block string) error {
	var body string
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		body = string(raw)
		if i := strings.Index(body, begin); i >= 0 {
			if j := strings.Index(body[i:], end); j >= 0 {
				j = i + j + len(end)
				// consume trailing newline
				for j < len(body) && (body[j] == '\n' || body[j] == '\r') {
					j++
				}
				body = body[:i] + body[j:]
			}
		}
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += block
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func removeMarkedBlock(path, begin, end string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	body := string(raw)
	i := strings.Index(body, begin)
	if i < 0 {
		return false, nil
	}
	jRel := strings.Index(body[i:], end)
	if jRel < 0 {
		return false, fmt.Errorf("%s: found begin marker without end", path)
	}
	j := i + jRel + len(end)
	for j < len(body) && (body[j] == '\n' || body[j] == '\r') {
		j++
	}
	body = body[:i] + body[j:]
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
