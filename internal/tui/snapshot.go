package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/plugins"
	"github.com/abyssmemes/contextverse/internal/version"
)

// Snapshot is read-only state for the client/solo TUI (refreshed from disk/CLI).
type Snapshot struct {
	SpaceRoot    string
	Mode         string
	HasConfig    bool
	IdentityName string
	IdentityRole string
	Layers       []LayerInfo
	Projects     []string
	Plugins      []PluginInfo
	Status       string
	LastMsg      string
	Err          string
	Output       string // last CLI action full output (for viewport)
}

// LayerInfo is a top-level space folder.
type LayerInfo struct {
	Name  string
	Files int
}

// PluginInfo is a client-integration row.
type PluginInfo struct {
	ID        string
	Mechanism string
	Display   string
	Detected  bool
	How       string
}

// LoadSnapshot gathers space + plugin state for display.
func LoadSnapshot(spaceRoot string) Snapshot {
	s := Snapshot{SpaceRoot: spaceRoot, Mode: string(config.ModeSolo)}
	if spaceRoot == "" {
		s.Err = "no space root"
		return s
	}
	if config.Exists(spaceRoot) {
		s.HasConfig = true
		if cfg, err := config.Load(spaceRoot); err == nil {
			s.Mode = string(cfg.Mode)
			s.IdentityName = cfg.Identity.Name
			s.IdentityRole = cfg.Identity.Role
		}
	}
	s.Layers = scanLayers(spaceRoot)
	s.Projects = listProjects(spaceRoot)
	s.Plugins = listPlugins(spaceRoot)
	meta := s.Mode
	if s.IdentityName != "" {
		meta = fmt.Sprintf("%s · %s", s.Mode, s.IdentityName)
	}
	s.Status = fmt.Sprintf("contextd %s · %s · %s", version.Version, meta, spaceRoot)
	return s
}

func scanLayers(root string) []LayerInfo {
	names := []string{"identity", "team", "projects"}
	var out []LayerInfo
	for _, n := range names {
		dir := filepath.Join(root, n)
		count := 0
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() {
				count++
			}
			return nil
		})
		out = append(out, LayerInfo{Name: n, Files: count})
	}
	return out
}

func listProjects(root string) []string {
	dir := filepath.Join(root, "projects")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func listPlugins(spaceRoot string) []PluginInfo {
	cat, err := plugins.DefaultCatalog("")
	if err != nil {
		return nil
	}
	vars, err := plugins.DefaultVars(spaceRoot, "", "")
	if err != nil {
		return nil
	}
	detected := map[string]string{}
	for _, d := range plugins.Detect(cat, vars) {
		detected[d.Integration.ID] = d.How
	}
	var out []PluginInfo
	for _, in := range cat {
		how, ok := detected[in.ID]
		out = append(out, PluginInfo{
			ID: in.ID, Mechanism: in.Mechanism, Display: in.Display,
			Detected: ok, How: how,
		})
	}
	return out
}

// RunAction executes a CLI-mapped action by invoking the same binary verbs via Go helpers / exec.
type Action string

const (
	ActionActivate     Action = "activate"
	ActionPluginInstall Action = "plugin-install"
	ActionStatus       Action = "status"
	ActionPull         Action = "pull"
	ActionPush         Action = "push"
)

// RunAction runs an action against the space (wrapper over CLI-equivalent ops).
func RunAction(a Action, spaceRoot, cwd string) (string, error) {
	bin, err := os.Executable()
	if err != nil {
		bin = "contextd"
	}
	switch a {
	case ActionActivate:
		cmd := exec.Command(bin, "activate", "--dir", spaceRoot, "--silent")
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	case ActionPluginInstall:
		cmd := exec.Command(bin, "plugin", "install", "--dir", spaceRoot)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	case ActionStatus:
		cmd := exec.Command(bin, "status", "--dir", spaceRoot)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	case ActionPull:
		cmd := exec.Command(bin, "pull", "--dir", spaceRoot)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	case ActionPush:
		cmd := exec.Command(bin, "push", "--dir", spaceRoot)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	default:
		return "", fmt.Errorf("unknown action %q", a)
	}
}
