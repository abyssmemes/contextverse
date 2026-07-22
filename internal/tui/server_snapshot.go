package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/version"
)

// ServerSnapshot is read-only admin state for the server TUI.
type ServerSnapshot struct {
	DataDir  string
	Listen   string
	Backend  string
	Space    string
	Spaces   []string
	Users    []string
	Policies []string
	Status   string
	LastMsg  string
	Err      string
	Output   string
	Exists   bool
}

// LoadServerSnapshot gathers server data-dir state.
func LoadServerSnapshot(dataDir string) ServerSnapshot {
	s := ServerSnapshot{DataDir: dataDir}
	if dataDir == "" {
		s.Err = "no server data dir"
		return s
	}
	if !config.ServerExists(dataDir) {
		s.Status = fmt.Sprintf("contextd %s · server not initialized at %s", version.Version, dataDir)
		s.Exists = false
		return s
	}
	s.Exists = true
	cfg, err := config.LoadServer(dataDir)
	if err != nil {
		s.Err = err.Error()
		return s
	}
	s.Listen = cfg.Addr()
	s.Backend = cfg.Backend.Driver
	if s.Backend == "" {
		s.Backend = "local"
	}
	switch cfg.Backend.Driver {
	case "git":
		if cfg.Backend.GitRemote != "" {
			s.Backend = fmt.Sprintf("%s · %s", s.Backend, cfg.Backend.GitRemote)
		}
	case "s3":
		if cfg.Backend.S3Bucket != "" {
			s.Backend = fmt.Sprintf("%s · %s", s.Backend, cfg.Backend.S3Bucket)
		}
	case "sql":
		if cfg.Backend.SQLDSN != "" {
			s.Backend = fmt.Sprintf("%s · (dsn set)", s.Backend)
		}
	}
	s.Space = cfg.Defaults.Space
	store, err := auth.OpenStore(dataDir)
	if err == nil {
		if users, err := store.ListUsers(); err == nil {
			for _, u := range users {
				pols := strings.Join(u.EffectivePolicies(), ",")
				s.Users = append(s.Users, fmt.Sprintf("%s\t%s", u.Name, pols))
			}
		}
		if eng, err := authz.Open(store.PoliciesDir()); err == nil {
			s.Policies = eng.List()
		}
	}
	spacesRoot := filepath.Join(dataDir, "spaces")
	if entries, err := os.ReadDir(spacesRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				s.Spaces = append(s.Spaces, e.Name())
			}
		}
	}
	s.Status = fmt.Sprintf("contextd %s · server · %s · backend=%s · listen=%s", version.Version, dataDir, s.Backend, s.Listen)
	return s
}

// ServerAction is a CLI-mapped server admin op.
type ServerAction string

const (
	ServerActionStatus  ServerAction = "server-status"
	ServerActionHealth  ServerAction = "server-health"
	ServerActionUsers   ServerAction = "user-list"
	ServerActionPolicies ServerAction = "policy-list"
)

// RunServerAction wraps CLI verbs for the server data dir.
func RunServerAction(a ServerAction, dataDir string) (string, error) {
	bin, err := os.Executable()
	if err != nil {
		bin = "contextd"
	}
	var cmd *exec.Cmd
	switch a {
	case ServerActionStatus:
		cmd = exec.Command(bin, "server", "status", "--server-dir", dataDir)
	case ServerActionHealth:
		cmd = exec.Command(bin, "server", "health", "--server-dir", dataDir)
	case ServerActionUsers:
		cmd = exec.Command(bin, "user", "list", "--server-dir", dataDir)
	case ServerActionPolicies:
		cmd = exec.Command(bin, "policy", "list", "--server-dir", dataDir)
	default:
		return "", fmt.Errorf("unknown server action %q", a)
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
