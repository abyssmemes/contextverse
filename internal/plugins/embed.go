package plugins

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/abyssmemes/contextverse/internal/logx"
)

//go:embed embed/*
var embeddedFS embed.FS

func loadFromEmbedFS(efs fs.ReadDirFS) ([]*Integration, error) {
	entries, err := fs.ReadDir(efs, "embed")
	if err != nil {
		return nil, err
	}
	var out []*Integration
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		raw, err := fs.ReadFile(efs, "embed/"+id+"/integration.yaml")
		if err != nil {
			logx.L().Warn("skip embedded integration", "id", id, "err", err)
			continue
		}
		var in Integration
		if err := yaml.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("embed %s: %w", id, err)
		}
		if in.ID == "" {
			in.ID = id
		}
		dir, err := materializeOne(efs, id)
		if err != nil {
			return nil, err
		}
		in.Dir = dir
		out = append(out, &in)
	}
	return out, nil
}

func materializeOne(efs fs.FS, id string) (string, error) {
	dir, err := os.MkdirTemp("", "cv-integ-"+id+"-*")
	if err != nil {
		return "", err
	}
	entries, err := fs.ReadDir(efs, "embed/"+id)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(efs, "embed/"+id+"/"+e.Name())
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), raw, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// DefaultCatalog loads embedded integrations, then optional extra on-disk root.
func DefaultCatalog(extraDir string) ([]*Integration, error) {
	out, err := loadFromEmbedFS(embeddedFS)
	if err != nil {
		return nil, err
	}
	if extraDir == "" {
		return out, nil
	}
	more, err := LoadCatalog(extraDir)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for _, in := range out {
		seen[in.ID] = true
	}
	for _, in := range more {
		if seen[in.ID] {
			continue
		}
		out = append(out, in)
	}
	return out, nil
}
