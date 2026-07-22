package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abyssmemes/contextverse/internal/logx"
)

// Catalog loads all integrations from dirs (embedded + optional extra roots).
func LoadCatalog(dirs ...string) ([]*Integration, error) {
	var out []*Integration
	seen := map[string]bool{}
	for _, root := range dirs {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			in, err := LoadIntegration(dir)
			if err != nil {
				logx.L().Warn("skip integration", "dir", dir, "err", err)
				continue
			}
			if seen[in.ID] {
				continue // first wins (embedded before remote)
			}
			seen[in.ID] = true
			out = append(out, in)
		}
	}
	return out, nil
}

// ApplyDetected detects installed clients and applies each matching integration.
// If none detected, prints manual fallback instructions.
func ApplyDetected(catalog []*Integration, vars Vars) ([]ApplyResult, error) {
	found := Detect(catalog, vars)
	if len(found) == 0 {
		fmt.Fprint(os.Stderr, ManualInstructions(nil, vars))
		return nil, nil
	}
	var results []ApplyResult
	for _, d := range found {
		res, err := Apply(d.Integration, vars)
		if err != nil {
			return results, fmt.Errorf("%s: %w", d.Integration.ID, err)
		}
		if res != nil {
			results = append(results, *res)
		}
		logx.L().Info("client detected", "id", d.Integration.ID, "via", d.How)
	}
	return results, nil
}

// ApplyByID applies a single catalog entry by id.
func ApplyByID(catalog []*Integration, id string, vars Vars) (*ApplyResult, error) {
	id = strings.TrimSpace(id)
	for _, in := range catalog {
		if in.ID == id {
			return Apply(in, vars)
		}
	}
	return nil, fmt.Errorf("unknown client-integration %q", id)
}
