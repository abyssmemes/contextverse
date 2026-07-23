//go:build !windows

package winsvc

import (
	"context"
	"fmt"
	"runtime"
)

// Name is the Windows service SCM name (documented for cross-platform CLI help).
const Name = "ContextVerse"

var errWindowsOnly = fmt.Errorf("contextd server service is Windows-only (this OS is %s); use systemd (deploy/contextd.service) or launchd (deploy/contextd.plist)", runtime.GOOS)

// IsService is always false outside Windows.
func IsService() bool { return false }

// Install returns a clear Windows-only error.
func Install(_, _ string) error { return errWindowsOnly }

// Uninstall returns a clear Windows-only error.
func Uninstall() error { return errWindowsOnly }

// Start returns a clear Windows-only error.
func Start() error { return errWindowsOnly }

// Stop returns a clear Windows-only error.
func Stop() error { return errWindowsOnly }

// Run returns a clear Windows-only error.
func Run(func(ctx context.Context) error) error { return errWindowsOnly }
