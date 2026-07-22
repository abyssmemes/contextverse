package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// FileMetaPrefix holds per-file version metadata JSON.
	FileMetaPrefix = "_filemeta/"
	// FileVerPrefix holds immutable per-version bodies.
	FileVerPrefix = "_filever/"
	// DefaultMaxFileVersions retained per path (oldest destroyed).
	DefaultMaxFileVersions = 10
)

// FileVersionInfo is one historical version of a path.
type FileVersionInfo struct {
	Version   int        `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	Hash      string     `json:"hash,omitempty"`
	Size      int        `json:"size"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Destroyed bool       `json:"destroyed,omitempty"`
}

// FileMeta is stored under _filemeta/<hex>.json.
type FileMeta struct {
	Path        string                     `json:"path"`
	Current     int                        `json:"current"` // 0 = soft-deleted / absent live
	MaxVersions int                        `json:"max_versions"`
	Versions    map[string]FileVersionInfo `json:"versions"`
	MetaCAS     Version                    `json:"-"`
}

// FileLog provides Vault KV v2-style per-file versioning over a Backend.
type FileLog struct {
	Backend     Backend
	MaxVersions int
}

func (f *FileLog) max() int {
	if f.MaxVersions <= 0 {
		return DefaultMaxFileVersions
	}
	return f.MaxVersions
}

func pathKey(path string) string {
	sum := sha256.Sum256([]byte(sanitizePath(path)))
	return hex.EncodeToString(sum[:16])
}

func metaPath(path string) string {
	return FileMetaPrefix + pathKey(path) + ".json"
}

func verBlobPath(path string, n int) string {
	return fmt.Sprintf("%s%s/%d", FileVerPrefix, pathKey(path), n)
}

// FormatFileVersion returns the CAS token string for version n.
func FormatFileVersion(n int) Version {
	if n <= 0 {
		return ""
	}
	return Version(strconv.Itoa(n))
}

// DisplayVersion formats a file CAS token for humans (v3). Hashes pass through unchanged.
func DisplayVersion(v Version) string {
	s := strings.TrimSpace(string(v))
	if s == "" {
		return "—"
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "v" + s
	}
	if strings.HasPrefix(s, "v") {
		if _, err := strconv.Atoi(s[1:]); err == nil {
			return s
		}
	}
	return s
}

// ParseFileVersion parses an integer file version (or empty = create).
func ParseFileVersion(v Version) (int, error) {
	s := strings.TrimSpace(string(v))
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: invalid file version %q", ErrInvalidArgument, v)
	}
	return n, nil
}

func (f *FileLog) loadMeta(ctx context.Context, path string) (*FileMeta, error) {
	raw, ver, err := f.Backend.Get(ctx, metaPath(path))
	if errors.Is(err, ErrNotFound) {
		return &FileMeta{
			Path:        sanitizePath(path),
			MaxVersions: f.max(),
			Versions:    map[string]FileVersionInfo{},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var m FileMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.Versions == nil {
		m.Versions = map[string]FileVersionInfo{}
	}
	if m.MaxVersions <= 0 {
		m.MaxVersions = f.max()
	}
	m.MetaCAS = ver
	m.Path = sanitizePath(path)
	return &m, nil
}

func (f *FileLog) saveMeta(ctx context.Context, m *FileMeta) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	next, err := f.Backend.Put(ctx, metaPath(m.Path), raw, m.MetaCAS)
	if err != nil {
		return err
	}
	m.MetaCAS = next
	return nil
}

func (f *FileLog) deleteBlob(ctx context.Context, path string) error {
	_, ver, err := f.Backend.Get(ctx, path)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Backend.Delete(ctx, path, ver)
}

func (f *FileLog) putLive(ctx context.Context, path string, data []byte) error {
	_, ver, err := f.Backend.Get(ctx, path)
	if errors.Is(err, ErrNotFound) {
		_, err = f.Backend.Put(ctx, path, data, "")
		return err
	}
	if err != nil {
		return err
	}
	_, err = f.Backend.Put(ctx, path, data, ver)
	return err
}

// adoptLegacy promotes an unversioned live blob to version 1.
func (f *FileLog) adoptLegacy(ctx context.Context, path string, m *FileMeta) (*FileMeta, error) {
	if m.Current != 0 || len(m.Versions) > 0 {
		return m, nil
	}
	data, _, err := f.Backend.Get(ctx, path)
	if errors.Is(err, ErrNotFound) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	hash := string(contentVersion(data))
	if _, err := f.Backend.Put(ctx, verBlobPath(path, 1), data, ""); err != nil {
		return m, err
	}
	m.Current = 1
	m.Versions["1"] = FileVersionInfo{
		Version:   1,
		CreatedAt: time.Now().UTC(),
		Hash:      hash,
		Size:      len(data),
	}
	if err := f.saveMeta(ctx, m); err != nil {
		return m, err
	}
	return f.loadMeta(ctx, path)
}

// LiveVersion returns the integer file version for the live path (no body read).
// Unversioned legacy blobs are reported as "1" (adopted on next Get/Put).
func (f *FileLog) LiveVersion(ctx context.Context, path string) (Version, error) {
	m, err := f.loadMeta(ctx, path)
	if err != nil {
		return "", err
	}
	if m.Current > 0 {
		return FormatFileVersion(m.Current), nil
	}
	_, _, err = f.Backend.Get(ctx, path)
	if err != nil {
		return "", err
	}
	// Live blob without meta → will become v1 on next FileLog write path.
	return FormatFileVersion(1), nil
}

// Get returns live content.
func (f *FileLog) Get(ctx context.Context, path string) ([]byte, Version, error) {
	m, err := f.loadMeta(ctx, path)
	if err != nil {
		return nil, "", err
	}
	m, err = f.adoptLegacy(ctx, path, m)
	if err != nil {
		return nil, "", err
	}
	if m.Current == 0 {
		data, ver, err := f.Backend.Get(ctx, path)
		if err != nil {
			return nil, "", err
		}
		return data, ver, nil
	}
	data, _, err := f.Backend.Get(ctx, path)
	if err != nil {
		return nil, "", err
	}
	return data, FormatFileVersion(m.Current), nil
}

// GetVersion returns a specific historical version body.
func (f *FileLog) GetVersion(ctx context.Context, path string, n int) ([]byte, *FileVersionInfo, error) {
	if n <= 0 {
		return nil, nil, fmt.Errorf("%w: version must be >= 1", ErrInvalidArgument)
	}
	m, err := f.loadMeta(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	m, err = f.adoptLegacy(ctx, path, m)
	if err != nil {
		return nil, nil, err
	}
	info, ok := m.Versions[strconv.Itoa(n)]
	if !ok || info.Destroyed {
		return nil, nil, ErrNotFound
	}
	data, _, err := f.Backend.Get(ctx, verBlobPath(path, n))
	if err != nil {
		return nil, nil, err
	}
	return data, &info, nil
}

// ListVersions returns version metadata (newest first).
func (f *FileLog) ListVersions(ctx context.Context, path string) (*FileMeta, []FileVersionInfo, error) {
	m, err := f.loadMeta(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	m, err = f.adoptLegacy(ctx, path, m)
	if err != nil {
		return nil, nil, err
	}
	out := make([]FileVersionInfo, 0, len(m.Versions))
	for _, v := range m.Versions {
		out = append(out, v)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Version > out[i].Version {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return m, out, nil
}

// Put appends a new version. CAS: expected "" = create; "N" = must match current;
// legacy content-hash tokens still accepted when they match the live blob.
func (f *FileLog) Put(ctx context.Context, path string, data []byte, expected Version) (Version, error) {
	path = sanitizePath(path)
	m, err := f.loadMeta(ctx, path)
	if err != nil {
		return "", err
	}
	m, err = f.adoptLegacy(ctx, path, m)
	if err != nil {
		return "", err
	}

	want, perr := ParseFileVersion(expected)
	if perr != nil {
		// Legacy content-hash If-Match
		live, liveVer, gerr := f.Backend.Get(ctx, path)
		if gerr == nil && liveVer == expected {
			_ = live
			want = m.Current
		} else {
			return "", perr
		}
	}

	if m.Current != want {
		return "", &ConflictError{Path: path, Expected: expected, Actual: FormatFileVersion(m.Current)}
	}

	next := m.Current + 1
	if m.Current == 0 {
		// After soft-delete, continue past highest retained version.
		for k := range m.Versions {
			n, _ := strconv.Atoi(k)
			if n >= next {
				next = n + 1
			}
		}
	}
	hash := string(contentVersion(data))
	if _, err := f.Backend.Put(ctx, verBlobPath(path, next), data, ""); err != nil {
		return "", err
	}
	if err := f.putLive(ctx, path, data); err != nil {
		return "", err
	}

	m.Current = next
	m.Versions[strconv.Itoa(next)] = FileVersionInfo{
		Version:   next,
		CreatedAt: time.Now().UTC(),
		Hash:      hash,
		Size:      len(data),
	}
	f.prune(ctx, m)
	if err := f.saveMeta(ctx, m); err != nil {
		return "", err
	}
	return FormatFileVersion(next), nil
}

func (f *FileLog) prune(ctx context.Context, m *FileMeta) {
	max := m.MaxVersions
	if max <= 0 {
		max = f.max()
	}
	type pair struct {
		n    int
		info FileVersionInfo
	}
	var all []pair
	for k, v := range m.Versions {
		if v.Destroyed {
			continue
		}
		n, _ := strconv.Atoi(k)
		all = append(all, pair{n, v})
	}
	if len(all) <= max {
		return
	}
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].n < all[i].n {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	drop := len(all) - max
	for i := 0; i < drop; i++ {
		n := all[i].n
		if n == m.Current {
			continue
		}
		_ = f.deleteBlob(ctx, verBlobPath(m.Path, n))
		info := m.Versions[strconv.Itoa(n)]
		info.Destroyed = true
		m.Versions[strconv.Itoa(n)] = info
	}
}

// SoftDelete removes the live file but keeps history.
func (f *FileLog) SoftDelete(ctx context.Context, path string, expected Version) error {
	m, err := f.loadMeta(ctx, path)
	if err != nil {
		return err
	}
	m, err = f.adoptLegacy(ctx, path, m)
	if err != nil {
		return err
	}
	want, err := f.resolveExpected(ctx, path, m, expected)
	if err != nil {
		return err
	}
	if m.Current != want {
		return &ConflictError{Path: path, Expected: expected, Actual: FormatFileVersion(m.Current)}
	}
	if m.Current == 0 {
		return ErrNotFound
	}
	now := time.Now().UTC()
	info := m.Versions[strconv.Itoa(m.Current)]
	info.DeletedAt = &now
	m.Versions[strconv.Itoa(m.Current)] = info
	if err := f.deleteBlob(ctx, path); err != nil {
		return err
	}
	m.Current = 0
	return f.saveMeta(ctx, m)
}

func (f *FileLog) resolveExpected(ctx context.Context, path string, m *FileMeta, expected Version) (int, error) {
	want, err := ParseFileVersion(expected)
	if err == nil {
		return want, nil
	}
	_, liveVer, gerr := f.Backend.Get(ctx, path)
	if gerr == nil && liveVer == expected {
		return m.Current, nil
	}
	return 0, err
}

// Undelete restores the highest non-destroyed version to live.
func (f *FileLog) Undelete(ctx context.Context, path string) (Version, error) {
	m, err := f.loadMeta(ctx, path)
	if err != nil {
		return "", err
	}
	best := 0
	for k, v := range m.Versions {
		if v.Destroyed {
			continue
		}
		n, _ := strconv.Atoi(k)
		if n > best {
			best = n
		}
	}
	if best == 0 {
		return "", ErrNotFound
	}
	data, _, err := f.Backend.Get(ctx, verBlobPath(path, best))
	if err != nil {
		return "", err
	}
	if err := f.putLive(ctx, path, data); err != nil {
		return "", err
	}
	info := m.Versions[strconv.Itoa(best)]
	info.DeletedAt = nil
	m.Versions[strconv.Itoa(best)] = info
	m.Current = best
	if err := f.saveMeta(ctx, m); err != nil {
		return "", err
	}
	return FormatFileVersion(best), nil
}

// Destroy permanently removes one version body.
func (f *FileLog) Destroy(ctx context.Context, path string, n int) error {
	if n <= 0 {
		return fmt.Errorf("%w: version", ErrInvalidArgument)
	}
	m, err := f.loadMeta(ctx, path)
	if err != nil {
		return err
	}
	key := strconv.Itoa(n)
	info, ok := m.Versions[key]
	if !ok {
		return ErrNotFound
	}
	if n == m.Current {
		return fmt.Errorf("%w: cannot destroy current live version — soft-delete first", ErrInvalidArgument)
	}
	_ = f.deleteBlob(ctx, verBlobPath(path, n))
	info.Destroyed = true
	m.Versions[key] = info
	return f.saveMeta(ctx, m)
}

// IsFileLogInternal reports reserved versioning keys.
func IsFileLogInternal(path string) bool {
	return strings.HasPrefix(path, FileMetaPrefix) || strings.HasPrefix(path, FileVerPrefix)
}
