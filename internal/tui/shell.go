package tui

import (
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

type shellDoneMsg struct {
	err error
}

type shellDeniedMsg struct{}

// hostShellAllowed is true for local TUI and Model A login (CONTEXTVERSE_MODEL_A=1).
// False under Model B Wish / plain SSH without the login marker — spawning a shell
// there would be a host/service-user shell escape.
func hostShellAllowed() bool {
	if os.Getenv("CONTEXTVERSE_MODEL_A") == "1" {
		return true
	}
	if os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_CLIENT") == "" {
		return true
	}
	return false
}

// spawnHostShellCmd suspends the TUI and runs $SHELL -l when allowed.
func spawnHostShellCmd() tea.Cmd {
	if !hostShellAllowed() {
		return func() tea.Msg { return shellDeniedMsg{} }
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	c := exec.Command(shell, "-l")
	c.Env = os.Environ()
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return shellDoneMsg{err: err}
	})
}

func shellDeniedFlash() string {
	return "shell escape disabled (Model B / non-login SSH); use contextd command mode or Model A login"
}
