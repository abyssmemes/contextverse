package spacesvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/hooks"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/space"
	"github.com/abyssmemes/contextverse/internal/storage"
)

// Meta is per-space metadata on the server.
type Meta struct {
	Name      string    `yaml:"name" json:"name"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	Template  string    `yaml:"template,omitempty" json:"template,omitempty"`
	Sync      SyncConfig `yaml:"sync" json:"sync"`
}

// SyncConfig holds selective sync rules.
type SyncConfig struct {
	Default string     `yaml:"default" json:"default"` // always|init-only|never
	Rules   []SyncRule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// SyncRule matches a path prefix or exact path.
type SyncRule struct {
	Path string `yaml:"path" json:"path"`
	Mode string `yaml:"mode" json:"mode"` // always|init-only|never
}

// Service manages spaces under <dataDir>/spaces.
type Service struct {
	DataDir string
	Backend config.Backend
	Hooks   hooks.Config
}

func (s *Service) spacesRoot() string {
	return filepath.Join(s.DataDir, "spaces")
}

// SpaceRoot returns the on-disk tree for a space.
func (s *Service) SpaceRoot(name string) string {
	return filepath.Join(s.spacesRoot(), name)
}

// OpenBackend opens the configured storage backend for a space.
func (s *Service) OpenBackend(name string) (storage.Backend, error) {
	return storage.Open(storage.OpenOptions{
		SpaceRoot: s.SpaceRoot(name),
		SpaceName: name,
		Backend:   s.Backend,
		Driver:    s.Backend.Driver,
	})
}

// List returns space names.
func (s *Service) List() ([]string, error) {
	root := s.spacesRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// LoadMeta reads meta.yaml.
func (s *Service) LoadMeta(name string) (*Meta, error) {
	raw, err := os.ReadFile(filepath.Join(s.SpaceRoot(name), "meta.yaml"))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Service) saveMeta(m *Meta) error {
	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	path := filepath.Join(s.SpaceRoot(m.Name), "meta.yaml")
	return os.WriteFile(path, raw, 0o644)
}

// DefaultSync returns the Phase-2a default selective sync rules.
func DefaultSync() SyncConfig {
	return SyncConfig{
		Default: "always",
		Rules: []SyncRule{
			{Path: "identity/", Mode: "init-only"},
			{Path: "team/", Mode: "always"},
			{Path: "projects/", Mode: "always"},
			{Path: "decisions.md", Mode: "always"},
		},
	}
}

// Create seeds a space from a template and snapshots into the local backend.
func (s *Service) Create(ctx context.Context, name, templateName string, force bool) (*Meta, error) {
	if name == "" {
		return nil, fmt.Errorf("space name required")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("invalid space name")
	}
	root := s.SpaceRoot(name)
	if !force {
		if _, err := os.Stat(root); err == nil {
			return nil, fmt.Errorf("space %q already exists", name)
		}
	}
	if templateName == "" {
		templateName = "solo-default"
	}
	if err := space.Create(space.CreateOptions{
		SpaceRoot:    root,
		TemplateName: templateName,
		Force:        force,
		Identity: space.IdentityFields{
			Name:     "Team",
			Role:     "shared",
			Language: "English",
		},
	}); err != nil {
		return nil, err
	}
	m := &Meta{
		Name:      name,
		CreatedAt: time.Now().UTC(),
		Template:  templateName,
		Sync:      DefaultSync(),
	}
	if err := s.saveMeta(m); err != nil {
		return nil, err
	}
	backend, err := s.OpenBackend(name)
	if err != nil {
		return nil, err
	}
	hist := &storage.History{Backend: backend}
	if _, err := hist.SnapshotSpace(ctx, root, "initial seed"); err != nil {
		return nil, fmt.Errorf("initial snapshot: %w", err)
	}
	logx.L().Info("space created on server", "space", name, "template", templateName)
	return m, nil
}

// Delete removes a space directory.
func (s *Service) Delete(name string) error {
	root := s.SpaceRoot(name)
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("space %q not found", name)
	}
	return os.RemoveAll(root)
}

// Head returns the space version marker.
func (s *Service) Head(ctx context.Context, name string) (storage.Version, error) {
	b, err := s.OpenBackend(name)
	if err != nil {
		return "", err
	}
	return b.Head(ctx, storage.SpaceScope)
}

func (s *Service) fileLog(name string) (*storage.FileLog, error) {
	b, err := s.OpenBackend(name)
	if err != nil {
		return nil, err
	}
	return &storage.FileLog{Backend: b}, nil
}

// Tree lists non-internal objects with FileLog integer versions.
func (s *Service) Tree(ctx context.Context, name string) ([]storage.Entry, error) {
	fl, err := s.fileLog(name)
	if err != nil {
		return nil, err
	}
	entries, err := fl.Backend.List(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []storage.Entry
	for _, e := range entries {
		if strings.HasPrefix(e.Path, storage.SnapshotPrefix) {
			continue
		}
		if storage.IsFileLogInternal(e.Path) {
			continue
		}
		if strings.HasPrefix(e.Path, "_health/") {
			continue
		}
		if ver, verr := fl.LiveVersion(ctx, e.Path); verr == nil {
			e.Version = ver
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// GetFile reads the live blob (integer file version as CAS token).
func (s *Service) GetFile(ctx context.Context, name, path string) ([]byte, storage.Version, error) {
	fl, err := s.fileLog(name)
	if err != nil {
		return nil, "", err
	}
	return fl.Get(ctx, path)
}

// GetFileVersion reads a historical version body.
func (s *Service) GetFileVersion(ctx context.Context, name, path string, n int) ([]byte, *storage.FileVersionInfo, error) {
	fl, err := s.fileLog(name)
	if err != nil {
		return nil, nil, err
	}
	return fl.GetVersion(ctx, path, n)
}

// ListFileVersions returns per-file version metadata (newest first).
func (s *Service) ListFileVersions(ctx context.Context, name, path string) (*storage.FileMeta, []storage.FileVersionInfo, error) {
	fl, err := s.fileLog(name)
	if err != nil {
		return nil, nil, err
	}
	return fl.ListVersions(ctx, path)
}

// PutFile writes with CAS (integer version) and mirrors to the working tree.
func (s *Service) PutFile(ctx context.Context, name, path string, data []byte, expected storage.Version) (storage.Version, error) {
	if err := s.Hooks.CheckPut(path, data); err != nil {
		return "", err
	}
	fl, err := s.fileLog(name)
	if err != nil {
		return "", err
	}
	ver, err := fl.Put(ctx, path, data, expected)
	if err != nil {
		return "", err
	}
	if err := writeTreeFile(s.SpaceRoot(name), path, data); err != nil {
		return ver, err
	}
	return ver, nil
}

// DeleteFile soft-deletes with CAS (history retained) and removes from tree.
func (s *Service) DeleteFile(ctx context.Context, name, path string, expected storage.Version) error {
	fl, err := s.fileLog(name)
	if err != nil {
		return err
	}
	if err := fl.SoftDelete(ctx, path, expected); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.SpaceRoot(name), filepath.FromSlash(path)))
	return nil
}

// UndeleteFile restores the latest non-destroyed version to live.
func (s *Service) UndeleteFile(ctx context.Context, name, path string) (storage.Version, error) {
	fl, err := s.fileLog(name)
	if err != nil {
		return "", err
	}
	ver, err := fl.Undelete(ctx, path)
	if err != nil {
		return "", err
	}
	data, _, err := fl.Get(ctx, path)
	if err != nil {
		return ver, err
	}
	if err := writeTreeFile(s.SpaceRoot(name), path, data); err != nil {
		return ver, err
	}
	return ver, nil
}

// DestroyFileVersion permanently removes one historical version.
func (s *Service) DestroyFileVersion(ctx context.Context, name, path string, n int) error {
	fl, err := s.fileLog(name)
	if err != nil {
		return err
	}
	return fl.Destroy(ctx, path, n)
}

// Change is one entry in a changes listing.
type Change struct {
	Path    string `json:"path"`
	Op      string `json:"op"` // put|delete
	Version string `json:"version"`
}

// Changes returns inventory when since differs from head (MVP: full put list).
func (s *Service) Changes(ctx context.Context, name, since string) ([]Change, storage.Version, error) {
	head, err := s.Head(ctx, name)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, "", err
	}
	if since != "" && storage.Version(since) == head {
		return nil, head, nil
	}
	entries, err := s.Tree(ctx, name)
	if err != nil {
		return nil, head, err
	}
	out := make([]Change, 0, len(entries))
	for _, e := range entries {
		out = append(out, Change{Path: e.Path, Op: "put", Version: string(e.Version)})
	}
	return out, head, nil
}

// PushOp is one operation in a batched push.
type PushOp struct {
	Op         string `json:"op"` // put|delete
	Path       string `json:"path"`
	ContentB64 string `json:"content_b64,omitempty"`
	Expected   string `json:"expected,omitempty"` // per-file version; optional
}

// PushRequest is the batched push body.
type PushRequest struct {
	ExpectedHead string   `json:"expected_head"`
	Ops          []PushOp `json:"ops"`
}

// PushResult is returned after a successful push.
type PushResult struct {
	Head    string `json:"head"`
	Applied int    `json:"applied"`
}

// Push applies ops transactionally against expected space head.
func (s *Service) Push(ctx context.Context, name string, req PushRequest) (*PushResult, error) {
	b, err := s.OpenBackend(name)
	if err != nil {
		return nil, err
	}
	cur, err := b.Head(ctx, storage.SpaceScope)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	if err == storage.ErrNotFound || errors.Is(err, storage.ErrNotFound) {
		cur = ""
	}
	if string(cur) != req.ExpectedHead {
		return nil, &storage.ConflictError{
			Path:     "head:" + storage.SpaceScope,
			Expected: storage.Version(req.ExpectedHead),
			Actual:   cur,
		}
	}

	fl := &storage.FileLog{Backend: b}
	applied := 0
	for _, op := range req.Ops {
		switch op.Op {
		case "put":
			data, err := decodeB64(op.ContentB64)
			if err != nil {
				return nil, fmt.Errorf("op put %s: %w", op.Path, err)
			}
			if err := s.Hooks.CheckPut(op.Path, data); err != nil {
				return nil, err
			}
			expected := storage.Version(op.Expected)
			if op.Expected == "" {
				// infer: create if absent, else require current
				if _, ver, gerr := fl.Get(ctx, op.Path); gerr == nil {
					expected = ver
				} else if !errors.Is(gerr, storage.ErrNotFound) {
					return nil, gerr
				}
			}
			if _, err := fl.Put(ctx, op.Path, data, expected); err != nil {
				return nil, err
			}
			if err := writeTreeFile(s.SpaceRoot(name), op.Path, data); err != nil {
				return nil, err
			}
			applied++
		case "delete":
			expected := storage.Version(op.Expected)
			if expected == "" {
				_, ver, gerr := fl.Get(ctx, op.Path)
				if gerr != nil {
					return nil, gerr
				}
				expected = ver
			}
			if err := fl.SoftDelete(ctx, op.Path, expected); err != nil {
				return nil, err
			}
			_ = os.Remove(filepath.Join(s.SpaceRoot(name), filepath.FromSlash(op.Path)))
			applied++
		default:
			return nil, fmt.Errorf("unknown op %q", op.Op)
		}
	}

	// Advance head to a new snapshot id-like token.
	next := newHeadID()
	if err := b.SetHead(ctx, storage.SpaceScope, cur, storage.Version(next)); err != nil {
		return nil, err
	}
	return &PushResult{Head: next, Applied: applied}, nil
}

func writeTreeFile(spaceRoot, path string, data []byte) error {
	abs := filepath.Join(spaceRoot, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

func decodeB64(s string) ([]byte, error) {
	if s == "" {
		return []byte{}, nil
	}
	return decodeStdB64(s)
}

func newHeadID() string {
	return time.Now().UTC().Format("20060102T150405Z") + "-" + randHex(4)
}
