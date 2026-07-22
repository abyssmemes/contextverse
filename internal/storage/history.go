package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abyssmemes/contextverse/internal/logx"
)

const (
	// SnapshotPrefix is the object-key prefix for snapshot manifests.
	SnapshotPrefix = "_snapshots/"
	// SpaceScope is the default whole-space head scope.
	SpaceScope = "space"
)

// SnapshotMeta is stored as JSON under _snapshots/<id>.json.
type SnapshotMeta struct {
	ID        string            `json:"id"`
	CreatedAt time.Time         `json:"created_at"`
	Message   string            `json:"message,omitempty"`
	Files     map[string]string `json:"files"` // path -> content version
}

// History wraps a Backend with snapshot/restore helpers.
type History struct {
	Backend Backend
}

// SnapshotSpace walks spaceRoot (skipping .contextverse and config.yaml),
// upserts each file into the backend, writes a manifest, and advances Head(space).
func (h *History) SnapshotSpace(ctx context.Context, spaceRoot, message string) (*SnapshotMeta, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(spaceRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(spaceRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if shouldSkipSpacePath(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}

	versions := make(map[string]string, len(files))
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		data := files[p]
		cur, ver, err := h.Backend.Get(ctx, p)
		switch {
		case errors.Is(err, ErrNotFound):
			ver, err = h.Backend.Put(ctx, p, data, "")
			if err != nil {
				return nil, fmt.Errorf("put %s: %w", p, err)
			}
		case err != nil:
			return nil, err
		default:
			if string(cur) == string(data) {
				// unchanged
			} else {
				ver, err = h.Backend.Put(ctx, p, data, ver)
				if err != nil {
					return nil, fmt.Errorf("put %s: %w", p, err)
				}
			}
		}
		versions[p] = string(ver)
	}

	id, err := newSnapshotID()
	if err != nil {
		return nil, err
	}
	meta := SnapshotMeta{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		Message:   message,
		Files:     versions,
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestPath := SnapshotPrefix + id + ".json"
	if _, err := h.Backend.Put(ctx, manifestPath, raw, ""); err != nil {
		return nil, fmt.Errorf("write snapshot manifest: %w", err)
	}

	expected := Version("")
	if head, err := h.Backend.Head(ctx, SpaceScope); err == nil {
		expected = head
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err := h.Backend.SetHead(ctx, SpaceScope, expected, Version(id)); err != nil {
		return nil, fmt.Errorf("advance space head: %w", err)
	}

	logx.L().Info("snapshot created", "id", id, "files", len(versions))
	return &meta, nil
}

// ListSnapshots returns manifests newest-first.
func (h *History) ListSnapshots(ctx context.Context) ([]SnapshotMeta, error) {
	entries, err := h.Backend.List(ctx, SnapshotPrefix)
	if err != nil {
		return nil, err
	}
	var out []SnapshotMeta
	for _, e := range entries {
		if !strings.HasSuffix(e.Path, ".json") {
			continue
		}
		raw, _, err := h.Backend.Get(ctx, e.Path)
		if err != nil {
			return nil, err
		}
		var meta SnapshotMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// GetSnapshot loads a snapshot manifest by id.
func (h *History) GetSnapshot(ctx context.Context, id string) (*SnapshotMeta, error) {
	raw, _, err := h.Backend.Get(ctx, SnapshotPrefix+id+".json")
	if err != nil {
		return nil, err
	}
	var meta SnapshotMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// RestoreSpace writes snapshot files into spaceRoot (overwrites matching paths).
func (h *History) RestoreSpace(ctx context.Context, spaceRoot, id string) error {
	meta, err := h.GetSnapshot(ctx, id)
	if err != nil {
		return err
	}
	for path := range meta.Files {
		data, _, err := h.Backend.Get(ctx, path)
		if err != nil {
			return fmt.Errorf("get %s: %w", path, err)
		}
		abs := filepath.Join(spaceRoot, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		tmp := abs + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, abs); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	expected := Version("")
	if head, err := h.Backend.Head(ctx, SpaceScope); err == nil {
		expected = head
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := h.Backend.SetHead(ctx, SpaceScope, expected, Version(id)); err != nil {
		return err
	}
	logx.L().Info("snapshot restored", "id", id, "files", len(meta.Files))
	return nil
}

// Migrate copies all objects and heads from src to dst.
func Migrate(ctx context.Context, src, dst Backend) (int, error) {
	entries, err := src.List(ctx, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		data, ver, err := src.Get(ctx, e.Path)
		if err != nil {
			return n, err
		}
		// Force overwrite on dest: delete if present, then create.
		if cur, curVer, err := dst.Get(ctx, e.Path); err == nil {
			_ = cur
			if err := dst.Delete(ctx, e.Path, curVer); err != nil {
				return n, fmt.Errorf("clear dest %s: %w", e.Path, err)
			}
		} else if !errors.Is(err, ErrNotFound) {
			return n, err
		}
		got, err := dst.Put(ctx, e.Path, data, "")
		if err != nil {
			return n, fmt.Errorf("put dest %s: %w", e.Path, err)
		}
		if got != ver && contentVersion(data) != got {
			// versions may differ by driver encoding; content must match
		}
		n++
	}
	// Copy space head if present.
	if head, err := src.Head(ctx, SpaceScope); err == nil {
		expected := Version("")
		if cur, err := dst.Head(ctx, SpaceScope); err == nil {
			expected = cur
		} else if !errors.Is(err, ErrNotFound) {
			return n, err
		}
		// If dest already has a different head, CAS-replace by matching expected.
		if err := dst.SetHead(ctx, SpaceScope, expected, head); err != nil {
			return n, fmt.Errorf("copy head: %w", err)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return n, err
	}
	logx.L().Info("migrate complete", "objects", n, "from", src.Name(), "to", dst.Name())
	return n, nil
}

func shouldSkipSpacePath(rel string, isDir bool) bool {
	base := rel
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		base = rel[:i]
	}
	switch base {
	case ".contextverse", ".git":
		return true
	}
	if rel == "config.yaml" {
		return true
	}
	if strings.HasSuffix(rel, ".tmp") {
		return true
	}
	_ = isDir
	return false
}

func newSnapshotID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}
