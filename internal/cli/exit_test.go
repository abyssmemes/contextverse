package cli

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/storage"
	"github.com/orkcom-tech/contextverse/internal/syncclient"
)

func TestExitCodeClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"plain error", errors.New("bad path"), ExitUsage},
		{"local CAS conflict", storage.ErrConflict, ExitConflict},
		{"wrapped CAS conflict", fmt.Errorf("edit lost the race: %w", storage.ErrConflict), ExitConflict},
		{"api 412", &syncclient.APIError{Status: 412, Code: "version_conflict"}, ExitConflict},
		{"api 409", &syncclient.APIError{Status: 409}, ExitConflict},
		{"api 401", &syncclient.APIError{Status: 401, Code: "unauthenticated"}, ExitNetwork},
		{"api 403", &syncclient.APIError{Status: 403, Code: "permission_denied"}, ExitNetwork},
		{"api 500", &syncclient.APIError{Status: 500}, ExitNetwork},
		{"api 404", &syncclient.APIError{Status: 404}, ExitUsage},
		{"wrapped api 401", fmt.Errorf("pull: %w", &syncclient.APIError{Status: 401}), ExitNetwork},
		{"explicit override", &ExitError{Code: ExitNetwork, Err: errors.New("x")}, ExitNetwork},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCodeFor(tc.err); got != tc.want {
				t.Errorf("ExitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// An unreachable server must be distinguishable from a typo in a path, or a
// script cannot tell "retry later" from "you called this wrong".
func TestNetworkErrorsAreExitTwo(t *testing.T) {
	_, err := net.Dial("tcp", "127.0.0.1:1")
	if err == nil {
		t.Skip("something is listening on port 1")
	}
	if got := ExitCodeFor(err); got != ExitNetwork {
		t.Errorf("dial failure = %d, want %d", got, ExitNetwork)
	}
	if got := ExitCodeFor(fmt.Errorf("pull: %w", err)); got != ExitNetwork {
		t.Errorf("wrapped dial failure = %d, want %d", got, ExitNetwork)
	}
}

func TestOutputFormatFlagsAreExclusive(t *testing.T) {
	t.Cleanup(func() { flagJSON, flagYAML = false, false })

	flagJSON, flagYAML = true, true
	if _, err := outFormat(); err == nil {
		t.Error("--json --yaml together should be rejected")
	}

	flagJSON, flagYAML = true, false
	f, err := outFormat()
	if err != nil {
		t.Fatal(err)
	}
	if !f.Structured() {
		t.Errorf("format = %q, want a structured one", f)
	}

	flagJSON, flagYAML = false, false
	f, _ = outFormat()
	if f.Structured() {
		t.Errorf("format = %q, want human by default", f)
	}
}
