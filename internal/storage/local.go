package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/abyssmemes/contextverse/internal/logx"
)

const (
	localMetaDir  = ".contextverse"
	localDataDir  = "objects"
	localHeadDir  = "heads"
	localLockFile = "store.lock"
)

// Local is a filesystem backend with flock + per-object CAS via content-hash versions.
type Local struct {
	root string // usually <space>/.contextverse
}

// OpenLocal creates/opens a local store under spaceRoot/.contextverse.
func OpenLocal(spaceRoot string) (*Local, error) {
	if spaceRoot == "" {
		return nil, fmt.Errorf("%w: empty space root", ErrInvalidArgument)
	}
	root := filepath.Join(spaceRoot, localMetaDir)
	for _, d := range []string{
		filepath.Join(root, localDataDir),
		filepath.Join(root, localHeadDir),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("create local store: %w", err)
		}
	}
	return &Local{root: root}, nil
}

func (l *Local) Name() string { return "local" }

func (l *Local) Root() string { return l.root }

func (l *Local) objectPath(path string) string {
	clean := sanitizePath(path)
	sum := sha256.Sum256([]byte(clean))
	key := hex.EncodeToString(sum[:])
	return filepath.Join(l.root, localDataDir, key[:2], key+".json")
}

type objectRecord struct {
	Path    string    `json:"path"`
	Version Version   `json:"version"`
	Data    []byte    `json:"data"`
	Updated time.Time `json:"updated"`
}

func (l *Local) withLock(ctx context.Context, fn func() error) error {
	lockPath := filepath.Join(l.root, localLockFile)
	lock := flock.New(lockPath)
	deadline := time.Now().Add(30 * time.Second)
	for {
		ok, err := lock.TryLock()
		if err != nil {
			return fmt.Errorf("lock store: %w", err)
		}
		if ok {
			defer func() { _ = lock.Unlock() }()
			return fn()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("lock store: timeout")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (l *Local) Get(ctx context.Context, path string) ([]byte, Version, error) {
	var data []byte
	var ver Version
	err := l.withLock(ctx, func() error {
		rec, err := l.readRecord(path)
		if err != nil {
			return err
		}
		data = append([]byte(nil), rec.Data...)
		ver = rec.Version
		return nil
	})
	return data, ver, err
}

func (l *Local) List(ctx context.Context, prefix string) ([]Entry, error) {
	var out []Entry
	err := l.withLock(ctx, func() error {
		root := filepath.Join(l.root, localDataDir)
		return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".json") {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			var rec objectRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
			if prefix != "" && !strings.HasPrefix(rec.Path, prefix) {
				return nil
			}
			out = append(out, Entry{Path: rec.Path, Version: rec.Version})
			return nil
		})
	})
	return out, err
}

func (l *Local) Put(ctx context.Context, path string, data []byte, expected Version) (Version, error) {
	var next Version
	err := l.withLock(ctx, func() error {
		rec, err := l.readRecord(path)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		actual := Version("")
		if err == nil {
			actual = rec.Version
		}
		if actual != expected {
			return &ConflictError{Path: path, Expected: expected, Actual: actual}
		}
		next = contentVersion(data)
		nrec := objectRecord{
			Path:    sanitizePath(path),
			Version: next,
			Data:    append([]byte(nil), data...),
			Updated: time.Now().UTC(),
		}
		return l.writeRecord(nrec)
	})
	if err != nil {
		return "", err
	}
	logx.L().Debug("local put", "path", path, "version", string(next))
	return next, nil
}

func (l *Local) Delete(ctx context.Context, path string, expected Version) error {
	return l.withLock(ctx, func() error {
		rec, err := l.readRecord(path)
		if err != nil {
			return err
		}
		if rec.Version != expected {
			return &ConflictError{Path: path, Expected: expected, Actual: rec.Version}
		}
		return os.Remove(l.objectPath(path))
	})
}

func (l *Local) Head(ctx context.Context, scope string) (Version, error) {
	var ver Version
	err := l.withLock(ctx, func() error {
		p := l.headPath(scope)
		raw, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				return ErrNotFound
			}
			return err
		}
		ver = Version(strings.TrimSpace(string(raw)))
		return nil
	})
	return ver, err
}

func (l *Local) SetHead(ctx context.Context, scope string, expected, next Version) error {
	return l.withLock(ctx, func() error {
		p := l.headPath(scope)
		actual := Version("")
		if raw, err := os.ReadFile(p); err == nil {
			actual = Version(strings.TrimSpace(string(raw)))
		} else if !os.IsNotExist(err) {
			return err
		}
		if actual != expected {
			return &ConflictError{Path: "head:" + scope, Expected: expected, Actual: actual}
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, []byte(string(next)+"\n"), 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, p)
	})
}

func (l *Local) headPath(scope string) string {
	s := sanitizePath(scope)
	if s == "" || s == "." {
		s = "_root"
	}
	sum := sha256.Sum256([]byte(s))
	return filepath.Join(l.root, localHeadDir, hex.EncodeToString(sum[:])+".head")
}

func (l *Local) readRecord(path string) (objectRecord, error) {
	raw, err := os.ReadFile(l.objectPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			return objectRecord{}, ErrNotFound
		}
		return objectRecord{}, err
	}
	var rec objectRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return objectRecord{}, err
	}
	return rec, nil
}

func (l *Local) writeRecord(rec objectRecord) error {
	p := l.objectPath(rec.Path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func sanitizePath(path string) string {
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "/")
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}
	return path
}

func contentVersion(data []byte) Version {
	sum := sha256.Sum256(data)
	return Version(hex.EncodeToString(sum[:8]))
}
