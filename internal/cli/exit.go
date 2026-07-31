package cli

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/orkcom-tech/contextverse/internal/cliout"
	"github.com/orkcom-tech/contextverse/internal/storage"
	"github.com/orkcom-tech/contextverse/internal/syncclient"
)

// Exit codes. Documented in contextverse-cli.md; a script needs to tell "your
// token expired" from "you typed the path wrong" from "someone else pushed
// first", and before this everything exited 1.
const (
	ExitOK       = 0
	ExitUsage    = 1 // usage or local state error
	ExitNetwork  = 2 // network, auth or permission failure talking to a server
	ExitConflict = 3 // compare-and-swap rejected (mapped from HTTP 412)
)

// ExitError carries the code a failure should exit with.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCodeFor classifies an error. Commands do not have to wrap anything: the
// classification is derived from the error's own type, so a new call site gets
// the right code without remembering to opt in.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}

	// Local compare-and-swap failure — same meaning as the server's 412.
	if errors.Is(err, storage.ErrConflict) {
		return ExitConflict
	}

	var apiErr *syncclient.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Conflict():
			return ExitConflict
		case apiErr.Unauthorized():
			return ExitNetwork
		case apiErr.Status >= 500:
			return ExitNetwork
		default:
			return ExitUsage
		}
	}

	// Transport-level failures: unreachable server, DNS, TLS, timeouts.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ExitNetwork
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ExitNetwork
	}

	return ExitUsage
}

// --- output format plumbing ---

var (
	flagJSON bool
	flagYAML bool
)

// outFormat resolves the global output flags.
func outFormat() (cliout.Format, error) { return cliout.Pick(flagJSON, flagYAML) }

// emit renders a command result: the typed value when a structured format was
// asked for, otherwise the human rendering.
func emit(w io.Writer, v any, human func(io.Writer) error) error {
	f, err := outFormat()
	if err != nil {
		return err
	}
	return cliout.Emit(w, f, v, human)
}

// structuredOutput reports whether the caller wants machine-readable output, so
// commands can keep progress lines off a JSON stream.
func structuredOutput() bool {
	f, err := outFormat()
	if err != nil {
		return false
	}
	return f.Structured()
}
