package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/storage"
)

// TrackedFile is one live path with its FileLog version (UI/CLI/TUI parity).
type TrackedFile struct {
	Path    string
	Version string // integer token, e.g. "1"
	Label   string // "path  v1"
}

// FileVersionRow is a display row for TUI version lists.
type FileVersionRow struct {
	Version   int
	Label     string
	Current   bool
	Destroyed bool
}

func openClientFileLog(spaceRoot string) (*storage.FileLog, error) {
	cfg, err := config.Load(spaceRoot)
	if err != nil {
		return nil, err
	}
	b, err := storage.Open(storage.OpenOptions{
		SpaceRoot: spaceRoot,
		Backend:   cfg.Backend,
		Driver:    cfg.Backend.Driver,
	})
	if err != nil {
		return nil, err
	}
	return &storage.FileLog{Backend: b}, nil
}

func openServerSpaceFileLog(dataDir, space string) (*storage.FileLog, error) {
	cfg, err := config.LoadServer(dataDir)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(dataDir, "spaces", space)
	b, err := storage.Open(storage.OpenOptions{
		SpaceRoot: root,
		Backend:   cfg.Backend,
		Driver:    cfg.Backend.Driver,
	})
	if err != nil {
		return nil, err
	}
	return &storage.FileLog{Backend: b}, nil
}

func listTrackedFiles(fl *storage.FileLog) ([]TrackedFile, error) {
	ctx := context.Background()
	entries, err := fl.Backend.List(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []TrackedFile
	for _, e := range entries {
		if strings.HasPrefix(e.Path, storage.SnapshotPrefix) || storage.IsFileLogInternal(e.Path) {
			continue
		}
		if strings.HasPrefix(e.Path, "_health/") || strings.HasPrefix(e.Path, "_heads/") {
			continue
		}
		ver := e.Version
		if lv, lerr := fl.LiveVersion(ctx, e.Path); lerr == nil {
			ver = lv
		}
		out = append(out, TrackedFile{
			Path:    e.Path,
			Version: string(ver),
			Label:   fmt.Sprintf("%s  %s", e.Path, storage.DisplayVersion(ver)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func listVersionRows(fl *storage.FileLog, path string) (current int, rows []FileVersionRow, err error) {
	meta, versions, err := fl.ListVersions(context.Background(), path)
	if err != nil {
		return 0, nil, err
	}
	for _, v := range versions {
		label := fmt.Sprintf("v%-4d  %s  %d B", v.Version, v.CreatedAt.Format("2006-01-02 15:04"), v.Size)
		if v.Destroyed {
			label += "  destroyed"
		}
		if v.DeletedAt != nil {
			label += "  deleted"
		}
		if v.Version == meta.Current {
			label += "  ← current"
		}
		rows = append(rows, FileVersionRow{
			Version:   v.Version,
			Label:     label,
			Current:   v.Version == meta.Current,
			Destroyed: v.Destroyed,
		})
	}
	return meta.Current, rows, nil
}

func revertFileVersion(fl *storage.FileLog, spaceRoot, path string, n int) (string, error) {
	ctx := context.Background()
	data, _, err := fl.GetVersion(ctx, path, n)
	if err != nil {
		return "", err
	}
	_, cur, err := fl.Get(ctx, path)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			return "", err
		}
		cur = ""
	}
	next, err := fl.Put(ctx, path, data, cur)
	if err != nil {
		return "", err
	}
	if spaceRoot != "" {
		abs := filepath.Join(spaceRoot, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err == nil {
			tmp := abs + ".tmp"
			if werr := os.WriteFile(tmp, data, 0o644); werr == nil {
				_ = os.Rename(tmp, abs)
			}
		}
	}
	return fmt.Sprintf("restored %s from v%d → %s", path, n, storage.DisplayVersion(next)), nil
}

func previewFileVersion(fl *storage.FileLog, path string, n int) (string, error) {
	data, info, err := fl.GetVersion(context.Background(), path, n)
	if err != nil {
		return "", err
	}
	body := string(data)
	if len(body) > 4000 {
		body = body[:4000] + "\n… (truncated)"
	}
	return fmt.Sprintf("%s @ v%d (%d bytes)\n\n%s", path, info.Version, info.Size, body), nil
}
