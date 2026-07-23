package winsvc_test

import (
	"testing"

	"github.com/abyssmemes/contextverse/internal/winsvc"
)

func TestNotServiceInUnitTest(t *testing.T) {
	if winsvc.IsService() {
		t.Fatal("IsService should be false in this test environment")
	}
}
