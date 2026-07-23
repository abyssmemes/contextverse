package winsvc_test

import (
	"testing"

	"github.com/abyssmemes/contextverse/internal/winsvc"
)

func TestStubNotService(t *testing.T) {
	if winsvc.IsService() {
		t.Fatal("IsService should be false in this test environment")
	}
}

func TestStubOpsError(t *testing.T) {
	if err := winsvc.Install("", "/tmp"); err == nil {
		t.Fatal("expected Windows-only error on non-Windows (or install without admin)")
	}
}
