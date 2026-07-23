//go:build windows

package winsvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// Name is the Windows service display / SCM name.
const Name = "ContextVerse"

// IsService reports whether this process was started by the Service Control Manager.
func IsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// Install registers contextd as a Windows service (requires admin).
func Install(exePath, serverDir string) error {
	if exePath == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil {
			return err
		}
	}
	exePath, err := filepath.Abs(exePath)
	if err != nil {
		return err
	}
	serverDir, err = filepath.Abs(serverDir)
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager (run as Administrator?): %w", err)
	}
	defer m.Disconnect()

	binArgs := []string{"server", "start", "--server-dir", serverDir, "--open=false"}
	s, err := m.OpenService(Name)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q already installed", Name)
	}
	s, err = m.CreateService(Name, exePath, mgr.Config{
		DisplayName: "ContextVerse contextd",
		Description: "ContextVerse context space server (contextd)",
		StartType:   mgr.StartAutomatic,
	}, binArgs...)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()
	_ = eventlog.InstallAsEventCreate(Name, eventlog.Error|eventlog.Warning|eventlog.Info)
	return nil
}

// Uninstall removes the Windows service.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager (run as Administrator?): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return fmt.Errorf("open service %q: %w", Name, err)
	}
	defer s.Close()
	_ = eventlog.Remove(Name)
	err = s.Delete()
	if err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	return nil
}

// Start starts the installed service.
func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return fmt.Errorf("open service %q: %w", Name, err)
	}
	defer s.Close()
	return s.Start()
}

// Stop stops the running service.
func Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return fmt.Errorf("open service %q: %w", Name, err)
	}
	defer s.Close()
	status, err := s.Control(svc.Stop)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for status.State != svc.Stopped {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for service stop (state=%v)", status.State)
		}
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return err
		}
	}
	return nil
}

// Run enters the SCM service loop and invokes serve until stop/shutdown.
func Run(serve func(ctx context.Context) error) error {
	return svc.Run(Name, &handler{serve: serve})
}

type handler struct {
	serve func(ctx context.Context) error
}

func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- h.serve(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepts}
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-errCh
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			default:
				// ignore
			}
		case err := <-errCh:
			changes <- svc.Status{State: svc.Stopped}
			if err != nil {
				return true, 1
			}
			return false, 0
		}
	}
}
