package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Mechanism kinds for session-start delivery.
const (
	MechanismCommandHook      = "command-hook"
	MechanismRulesSlot        = "rules-slot"
	MechanismInstructionsSlot = "instructions-slot"
	MechanismManual           = "manual"
)

// DetectProbe is one any-match detection rule.
type DetectProbe struct {
	Path string `yaml:"path,omitempty"`
	Bin  string `yaml:"bin,omitempty"`
}

// Integration is a client-integration template (machine-readable).
type Integration struct {
	ID        string        `yaml:"id"`
	Display   string        `yaml:"display"`
	Detect    []DetectProbe `yaml:"detect"`
	Mechanism string        `yaml:"mechanism"`
	Target    string        `yaml:"target"`
	Merge     string        `yaml:"merge"`
	Command   string        `yaml:"command,omitempty"`
	Payload   string        `yaml:"payload,omitempty"`
	Notes     string        `yaml:"notes,omitempty"`
	Manual    string        `yaml:"manual,omitempty"` // paste-block for --list / fallback

	// Dir is the on-disk template directory (set when loaded).
	Dir string `yaml:"-"`
}

// Vars for expanding targets and payloads.
type Vars struct {
	Home    string
	Cwd     string
	Space   string
	Project string
}

// Expand replaces {{home}}, {{cwd}}, {{space}}, {{project}} (and ~).
func Expand(s string, v Vars) string {
	out := s
	if strings.HasPrefix(out, "~/") {
		out = filepath.Join(v.Home, out[2:])
	} else if out == "~" {
		out = v.Home
	}
	repl := map[string]string{
		"{{home}}":    v.Home,
		"{{cwd}}":     v.Cwd,
		"{{space}}":   v.Space,
		"{{project}}": v.Project,
		"{{HOME}}":    v.Home,
		"{{CWD}}":     v.Cwd,
	}
	for k, val := range repl {
		out = strings.ReplaceAll(out, k, val)
	}
	return out
}

// LoadIntegration reads integration.yaml from a template directory.
func LoadIntegration(dir string) (*Integration, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "integration.yaml"))
	if err != nil {
		return nil, err
	}
	var in Integration
	if err := yaml.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("parse integration.yaml: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("integration.yaml: id required")
	}
	if in.Mechanism == "" {
		return nil, fmt.Errorf("integration %s: mechanism required", in.ID)
	}
	in.Dir = dir
	return &in, nil
}

// DefaultVars builds vars from space root and cwd.
func DefaultVars(spaceRoot, cwd, project string) (Vars, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Vars{}, err
	}
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return Vars{}, err
		}
	}
	absSpace := spaceRoot
	if spaceRoot != "" {
		absSpace, err = filepath.Abs(spaceRoot)
		if err != nil {
			return Vars{}, err
		}
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return Vars{}, err
	}
	return Vars{Home: home, Cwd: absCwd, Space: absSpace, Project: project}, nil
}
