package plugins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Detected is an integration that matched a probe on this machine.
type Detected struct {
	Integration *Integration
	How         string // e.g. path:~/.claude/ or bin:claude
}

// Detect runs any-match probes for each integration.
func Detect(list []*Integration, vars Vars) []Detected {
	var out []Detected
	for _, in := range list {
		if in == nil {
			continue
		}
		if how, ok := matchDetect(in.Detect, vars); ok {
			out = append(out, Detected{Integration: in, How: how})
		}
	}
	return out
}

func matchDetect(probes []DetectProbe, vars Vars) (string, bool) {
	for _, p := range probes {
		if p.Path != "" {
			path := Expand(p.Path, vars)
			if st, err := os.Stat(path); err == nil && st.IsDir() {
				return "path:" + p.Path, true
			}
			// also accept file
			if _, err := os.Stat(path); err == nil {
				return "path:" + p.Path, true
			}
		}
		if p.Bin != "" {
			if _, err := exec.LookPath(p.Bin); err == nil {
				return "bin:" + p.Bin, true
			}
		}
	}
	return "", false
}

// ResolveProject guesses projects/<name> from cwd if under spaceRoot/projects.
func ResolveProject(spaceRoot, cwd string) string {
	if spaceRoot == "" || cwd == "" {
		return ""
	}
	absSpace, err := filepath.Abs(spaceRoot)
	if err != nil {
		return ""
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	prefix := filepath.Join(absSpace, "projects") + string(os.PathSeparator)
	if !strings.HasPrefix(absCwd, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(absCwd, prefix)
	parts := strings.Split(rest, string(os.PathSeparator))
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}
