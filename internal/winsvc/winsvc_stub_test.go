//go:build !windows

package winsvc_test

import (
	"testing"

	"github.com/abyssmemes/contextverse/internal/winsvc"
)

func TestStubOpsError(t *testing.T) {
	if err := winsvc.Install("", "/tmp"); err == nil {
		t.Fatal("expected Windows-only error on non-Windows")
	}
	if err := winsvc.Uninstall(); err == nil {
		t.Fatal("expected Windows-only error on non-Windows")
	}
	if err := winsvc.Start(); err == nil {
		t.Fatal("expected Windows-only error on non-Windows")
	}
	if err := winsvc.Stop(); err == nil {
		t.Fatal("expected Windows-only error on non-Windows")
	}
}
