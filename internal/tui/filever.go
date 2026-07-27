package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/spacefiles"
	"github.com/abyssmemes/contextverse/internal/storage"
)

// TrackedFile is one live path with its FileLog version (UI/CLI/TUI parity).
type TrackedFile struct {
	Path      string
	Version   string // integer token, e.g. "1"
	Label     string // "path  v1", or "path  —" when no version exists yet
	Untracked bool   // on disk, but no version recorded yet
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

// listSpaceFiles lists what is in the space, not merely what the version log
// knows about.
//
// Listing only tracked files produced the contradiction a real install ran
// into: the Space tab counted documents on disk while the Files tab said "(no
// tracked files)" and offered nothing to open. A file you can see in your own
// directory has to be openable, whether or not contextd has recorded a version
// of it yet — editing it is what creates one.
func listSpaceFiles(fl *storage.FileLog, spaceRoot string) ([]TrackedFile, error) {
	entries, err := spacefiles.List(context.Background(), fl, spaceRoot)
	if err != nil {
		return nil, err
	}
	out := make([]TrackedFile, 0, len(entries))
	for _, e := range entries {
		shown := e.Display()
		if shown == "" {
			// Not an error state, just a file whose first version has not been
			// written yet. Saying so beats hiding it.
			shown = "—"
		}
		out = append(out, TrackedFile{
			Path:      e.Path,
			Version:   string(e.Version),
			Label:     fmt.Sprintf("%s  %s", e.Path, shown),
			Untracked: !e.Tracked,
		})
	}
	return out, nil
}

func skipStoragePath(p string) bool {
	return strings.HasPrefix(p, storage.SnapshotPrefix) ||
		storage.IsFileLogInternal(p) ||
		strings.HasPrefix(p, "_health/") ||
		strings.HasPrefix(p, "_heads/")
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
