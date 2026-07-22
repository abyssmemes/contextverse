//go:build unix

package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/abyssmemes/contextverse/internal/logx"
)

// ignoreTerminalStopSignals prevents Ctrl+Z (SIGTSTP) from suspending the
// server. A suspended process still holds the listen port and accepts TCP
// without answering — browsers hang forever.
func ignoreTerminalStopSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTSTP)
	go func() {
		for range ch {
			logx.L().Warn("ignoring Ctrl+Z (SIGTSTP) — use Ctrl+C or: contextd server stop")
		}
	}()
}
