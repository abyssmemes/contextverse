package spacesvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/orkcom-tech/contextverse/internal/config"
	"github.com/orkcom-tech/contextverse/internal/hooks"
	"github.com/orkcom-tech/contextverse/internal/logx"
	"github.com/orkcom-tech/contextverse/internal/quotas"
	"github.com/orkcom-tech/contextverse/internal/space"
	"github.com/orkcom-tech/contextverse/internal/storage"
)

// Meta is per-space metadata on the server.
type Meta struct {
	Name      string     `yaml:"name" json:"name"`
	CreatedAt time.Time  `yaml:"created_at" json:"created_at"`
	Template  string     `yaml:"template,omitempty" json:"template,omitempty"`
	Sync      SyncConfig `yaml:"sync" json:"sync"`
	// Quotas overrides the server's limits for this space alone. Any field left
	// at zero inherits the server value, so a space can raise one limit without
	// restating the others.
	//
	// Server-wide limits are the right default and the wrong only option: one
	// server can hold a canonical space that should stay small and a scratch
	// space that should not, and a hosted fleet cannot give two customers on
	// one instance different plans without this.
	Quotas quotas.Config `yaml:"quotas,omitempty" json:"quotas,omitempty"`
}

// QuotasFor resolves the limits that apply to one space: the server's, with any
// per-space override laid over the top.
//
// A space with no meta.yaml, or an unreadable one, falls back to the server
// limits rather than to no limits. Failing open on a quota check is how a
// misconfigured space becomes an unbounded one.
func (s *Service) QuotasFor(name string) quotas.Config {
	meta, err := s.LoadMeta(name)
	if err != nil {
		return s.Quotas.WithDefaults()
	}
	out := s.Quotas
	if meta.Quotas.MaxFileSize > 0 {
		out.MaxFileSize = meta.Quotas.MaxFileSize
	}
	if meta.Quotas.MaxSpaceSize > 0 {
		out.MaxSpaceSize = meta.Quotas.MaxSpaceSize
	}
	if meta.Quotas.MaxFiles > 0 {
		out.MaxFiles = meta.Quotas.MaxFiles
	}
	// Resolved, not raw: a zero here means "inherit the default", and returning
	// it unresolved tells a caller their limit is none.
	return out.WithDefaults()
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
	Quotas  quotas.Config

	// pool keeps opened backends alive across requests. Created on first use so
	// the zero Service still works for the short-lived CLI commands that build
	// one, do a thing and exit.
	poolOnce sync.Once
	pool     *storage.Pool

	// locks serializes the mutating operations of one space against each other.
	// The head's compare-and-swap protects the marker, not the writes behind it:
	// two pushes could both read the same head, both write their files, and only
	// then have one told it lost. One writer at a time per space is what makes a
	// batch mean anything.
	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

// lockSpace takes the space's write lock and returns its release.
//
// Only the mutating entry points take it. Nothing they call may take it again:
// the quota inventory reads through Tree, which stays lock-free on purpose.
func (s *Service) lockSpace(name string) func() {
	s.locksMu.Lock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	mu, ok := s.locks[name]
	if !ok {
		mu = &sync.Mutex{}
		s.locks[name] = mu
	}
	s.locksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

func (s *Service) backends() *storage.Pool {
	s.poolOnce.Do(func() { s.pool = storage.NewPool() })
	return s.pool
}

// Close releases the storage connections the service holds. A long-lived
// process (the server) must call it; a one-shot command need not.
func (s *Service) Close() error {
	if s == nil || s.pool == nil {
		return nil
	}
	return s.pool.Close()
}

func (s *Service) spacesRoot() string {
	return filepath.Join(s.DataDir, "spaces")
}

// SpaceRoot returns the on-disk tree for a space. An invalid name yields a path
// that cannot exist, so callers that skipped SpaceRootFor still fail closed
// instead of reaching a sibling directory.
func (s *Service) SpaceRoot(name string) string {
	if storage.ValidSpaceName(name) != nil {
		return filepath.Join(s.spacesRoot(), "_invalid-space-name")
	}
	return filepath.Join(s.spacesRoot(), name)
}

// SpaceRootFor validates the name and returns its on-disk tree.
func (s *Service) SpaceRootFor(name string) (string, error) {
	if err := storage.ValidSpaceName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.spacesRoot(), name), nil
}

// treePath resolves a caller-supplied blob path inside a space's working tree.
func (s *Service) treePath(name, rel string) (string, error) {
	root, err := s.SpaceRootFor(name)
	if err != nil {
		return "", err
	}
	return storage.ResolveUnder(root, rel)
}

// OpenBackend returns the storage backend for a space.
//
// Pooled, not opened: this is called several times to serve one write — the
// put, the head bump, the quota walk — and opening a Postgres pool or an S3
// client each time is how a server runs out of database connections while
// looking idle.
func (s *Service) OpenBackend(name string) (storage.Backend, error) {
	return s.backends().Open(storage.OpenOptions{
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
	root, err := s.SpaceRootFor(name)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(root, "meta.yaml"))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SaveMeta writes a space's metadata back. Exported so the CLI can change sync
// rules: which paths travel is a policy decision an operator makes, and it was
// previously only editable by hand-editing meta.yaml on the server.
func (s *Service) SaveMeta(m *Meta) error { return s.saveMeta(m) }

func (s *Service) saveMeta(m *Meta) error {
	root, err := s.SpaceRootFor(m.Name)
	if err != nil {
		return err
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "meta.yaml"), raw, 0o644)
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
	root, err := s.SpaceRootFor(name)
	if err != nil {
		return nil, err
	}
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
	root, err := s.SpaceRootFor(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("space %q not found", name)
	}
	// Drop the pooled handle first: a space recreated under the same name must
	// not inherit a connection to the one that was just removed.
	s.backends().Evict(name)
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
	unlock := s.lockSpace(name)
	defer unlock()
	if err := s.Hooks.CheckPut(path, data); err != nil {
		return "", err
	}
	if err := s.QuotasFor(name).CheckFileSize(int64(len(data))); err != nil {
		return "", err
	}
	if err := s.checkSpaceQuota(ctx, name, int64(len(data)), path); err != nil {
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
	if err := s.writeTreeFile(name, path, data); err != nil {
		return ver, err
	}
	return ver, nil
}

// checkSpaceQuota decides whether one write fits, using the same inventory the
// batch path uses.
//
// It fails closed. The previous version returned nil whenever the listing
// failed — "don't block on list failure" — which turned an unreadable space
// into an unlimited one, the very thing QuotasFor's comment says not to do.
func (s *Service) checkSpaceQuota(ctx context.Context, name string, newBytes int64, path string) error {
	sizes, total, err := s.inventory(ctx, name)
	if err != nil {
		return err
	}
	old, exists := sizes[path]
	deltaFiles := 0
	if !exists {
		deltaFiles = 1
	}
	return s.QuotasFor(name).CheckSpace(total, len(sizes), newBytes-old, deltaFiles)
}

// SpaceUsage returns approximate on-disk bytes and file count for quota warnings.
func (s *Service) SpaceUsage(ctx context.Context, name string) (bytes int64, files int, err error) {
	entries, err := s.Tree(ctx, name)
	if err != nil {
		return 0, 0, err
	}
	// Same reasoning as inventory: the backend knows what it holds, and the
	// working-tree mirror only exists on whichever replica did the writing.
	for _, e := range entries {
		if e.Size > 0 {
			bytes += e.Size
			continue
		}
		if p, perr := s.treePath(name, e.Path); perr == nil {
			if st, serr := os.Stat(p); serr == nil {
				bytes += st.Size()
			}
		}
	}
	return bytes, len(entries), nil
}

// DeleteFile soft-deletes with CAS (history retained) and removes from tree.
func (s *Service) DeleteFile(ctx context.Context, name, path string, expected storage.Version) error {
	unlock := s.lockSpace(name)
	defer unlock()
	fl, err := s.fileLog(name)
	if err != nil {
		return err
	}
	if err := fl.SoftDelete(ctx, path, expected); err != nil {
		return err
	}
	s.removeTreeFile(name, path)
	return nil
}

// UndeleteFile restores the latest non-destroyed version to live.
func (s *Service) UndeleteFile(ctx context.Context, name, path string) (storage.Version, error) {
	unlock := s.lockSpace(name)
	defer unlock()
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
	if err := s.writeTreeFile(name, path, data); err != nil {
		return ver, err
	}
	return ver, nil
}

// DestroyFileVersion permanently removes one historical version.
func (s *Service) DestroyFileVersion(ctx context.Context, name, path string, n int) error {
	unlock := s.lockSpace(name)
	defer unlock()
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

// writeTreeFile mirrors a blob into the space's working tree. The path is
// resolved under the space root, so a request for "../other/notes.md" is
// rejected rather than written next door.
func (s *Service) writeTreeFile(name, path string, data []byte) error {
	abs, err := s.treePath(name, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

// removeTreeFile drops the working-tree mirror; a rejected path is a no-op
// because nothing inside the space could have been written under it.
func (s *Service) removeTreeFile(name, path string) {
	abs, err := s.treePath(name, path)
	if err != nil {
		logx.L().Warn("refused tree path", "space", name, "path", path, "err", err)
		return
	}
	_ = os.Remove(abs)
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
