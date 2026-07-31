package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/orkcom-tech/contextverse/internal/logx"
)

const (
	gitObjectsPrefix = "objects"
	gitHeadsPrefix   = "heads"
	gitRemoteName    = "origin"
)

// GitConfig configures the git storage backend.
type GitConfig struct {
	// LocalPath is the working tree for the store (usually <space>/.contextverse/git).
	LocalPath string
	// RemoteURL is optional; when set, Test and Push attempt to reach it.
	RemoteURL string
	// Auth for private remotes (HTTPS token or SSH key).
	Auth GitAuth
	// AutoPush pushes to remote after each mutating op when RemoteURL is set.
	AutoPush bool
}

// Git stores blobs as files in a git working tree. Head markers are plain files;
// commits provide durability and optional remote mirror.
type Git struct {
	path     string
	remote   string
	auth     GitAuth
	autoPush bool
	repo     *git.Repository

	// mu serializes every operation that touches the repository.
	//
	// This backend had no locking at all while Local took a flock for each one,
	// and the interface it implements promises compare-and-swap. Two things
	// break without it. The compare and the swap were separate — read the file,
	// compare its hash, write — so two writers both passed and the second
	// silently discarded the first. And go-git's worktree is not safe for
	// concurrent use: Add and Commit share one index file, so overlapping
	// commits corrupt it or lose a change that was staged and never committed.
	//
	// One lock rather than one per path, because the index and HEAD are the
	// repository's, not any single file's. A commit per write is already the
	// slow part; contending on the lock does not change that.
	mu sync.Mutex
}

// OpenGit opens or initializes a git backend at cfg.LocalPath.
func OpenGit(cfg GitConfig) (*Git, error) {
	if cfg.LocalPath == "" {
		return nil, fmt.Errorf("%w: empty git local path", ErrInvalidArgument)
	}
	if err := os.MkdirAll(cfg.LocalPath, 0o755); err != nil {
		return nil, err
	}

	repo, err := git.PlainOpen(cfg.LocalPath)
	if err == git.ErrRepositoryNotExists {
		repo, err = git.PlainInit(cfg.LocalPath, false)
		if err != nil {
			return nil, fmt.Errorf("init git store: %w", err)
		}
		if err := seedEmptyCommit(repo); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("open git store: %w", err)
	}

	auto := cfg.RemoteURL != "" && cfg.AutoPush

	g := &Git{path: cfg.LocalPath, remote: cfg.RemoteURL, auth: cfg.Auth, autoPush: auto, repo: repo}
	if cfg.RemoteURL != "" {
		if err := g.ensureRemote(cfg.RemoteURL); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func seedEmptyCommit(repo *git.Repository) error {
	w, err := repo.Worktree()
	if err != nil {
		return err
	}
	keep := filepath.Join(w.Filesystem.Root(), ".gitkeep")
	if err := os.WriteFile(keep, []byte{}, 0o644); err != nil {
		return err
	}
	if _, err := w.Add(".gitkeep"); err != nil {
		return err
	}
	_, err = w.Commit("contextverse: init store", &git.CommitOptions{
		Author: defaultSignature(),
	})
	return err
}

func defaultSignature() *object.Signature {
	return &object.Signature{
		Name:  "contextd",
		Email: "contextd@localhost",
		When:  time.Now().UTC(),
	}
}

func (g *Git) Name() string { return "git" }

func (g *Git) LocalPath() string { return g.path }

func (g *Git) RemoteURL() string { return g.remote }

func (g *Git) ensureRemote(url string) error {
	rem, err := g.repo.Remote(gitRemoteName)
	if err == git.ErrRemoteNotFound {
		_, err = g.repo.CreateRemote(&config.RemoteConfig{
			Name: gitRemoteName,
			URLs: []string{url},
		})
		return err
	}
	if err != nil {
		return err
	}
	cfg := rem.Config()
	if len(cfg.URLs) == 1 && cfg.URLs[0] == url {
		return nil
	}
	if err := g.repo.DeleteRemote(gitRemoteName); err != nil {
		return err
	}
	_, err = g.repo.CreateRemote(&config.RemoteConfig{
		Name: gitRemoteName,
		URLs: []string{url},
	})
	return err
}

// objectAbs resolves a validated path inside the object tree. It re-checks
// containment because this backend writes real files.
func (g *Git) objectAbs(path string) (string, error) {
	return ResolveUnder(filepath.Join(g.path, gitObjectsPrefix), sanitizePath(path)+".bin")
}

func (g *Git) objectRel(path string) string {
	return filepath.ToSlash(filepath.Join(gitObjectsPrefix, sanitizePath(path)+".bin"))
}

func (g *Git) headAbs(scope string) string {
	s := sanitizePath(scope)
	if s == "" || s == "." {
		s = "_root"
	}
	return filepath.Join(g.path, gitHeadsPrefix, string(contentVersion([]byte(s)))+".head")
}

func (g *Git) headRel(scope string) string {
	s := sanitizePath(scope)
	if s == "" || s == "." {
		s = "_root"
	}
	return filepath.ToSlash(filepath.Join(gitHeadsPrefix, string(contentVersion([]byte(s)))+".head"))
}

func (g *Git) commit(paths []string, msg string) error {
	w, err := g.repo.Worktree()
	if err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := w.Add(p); err != nil {
			// deleted paths use Remove
			if _, rerr := w.Remove(p); rerr != nil {
				return err
			}
		}
	}
	_, err = w.Commit(msg, &git.CommitOptions{Author: defaultSignature()})
	if err == git.ErrEmptyCommit {
		return nil
	}
	return err
}

func (g *Git) Get(ctx context.Context, path string) ([]byte, Version, error) {
	_ = ctx
	g.mu.Lock()
	defer g.mu.Unlock()
	path, err := CleanFilePath(path)
	if err != nil {
		return nil, "", err
	}
	abs, err := g.objectAbs(path)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	return data, contentVersion(data), nil
}

func (g *Git) List(ctx context.Context, prefix string) ([]Entry, error) {
	_ = ctx
	g.mu.Lock()
	defer g.mu.Unlock()
	prefix, err := CleanPath(prefix)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(g.path, gitObjectsPrefix)
	var out []Entry
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".bin") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		rel = strings.TrimSuffix(rel, ".bin")
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, Entry{Path: rel, Version: contentVersion(data)})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return out, nil
}

func (g *Git) Put(ctx context.Context, path string, data []byte, expected Version) (Version, error) {
	_ = ctx
	// The read, the compare and the write are one operation or they are a
	// lost update.
	g.mu.Lock()
	defer g.mu.Unlock()
	path, err := CleanFilePath(path)
	if err != nil {
		return "", err
	}
	abs, err := g.objectAbs(path)
	if err != nil {
		return "", err
	}
	rel := g.objectRel(path)

	actual := Version("")
	if cur, err := os.ReadFile(abs); err == nil {
		actual = contentVersion(cur)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if actual != expected {
		return "", &ConflictError{Path: path, Expected: expected, Actual: actual}
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, abs); err != nil {
		return "", err
	}
	if err := g.commit([]string{rel}, fmt.Sprintf("contextverse: put %s", sanitizePath(path))); err != nil {
		return "", err
	}
	next := contentVersion(data)
	logx.L().Debug("git put", "path", path, "version", string(next))
	if err := g.maybePush(ctx); err != nil {
		return next, fmt.Errorf("put ok but push failed: %w", err)
	}
	return next, nil
}

func (g *Git) Delete(ctx context.Context, path string, expected Version) error {
	_ = ctx
	g.mu.Lock()
	defer g.mu.Unlock()
	path, err := CleanFilePath(path)
	if err != nil {
		return err
	}
	abs, err := g.objectAbs(path)
	if err != nil {
		return err
	}
	rel := g.objectRel(path)
	cur, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	actual := contentVersion(cur)
	if actual != expected {
		return &ConflictError{Path: path, Expected: expected, Actual: actual}
	}
	if err := os.Remove(abs); err != nil {
		return err
	}
	if err := g.commit([]string{rel}, fmt.Sprintf("contextverse: delete %s", sanitizePath(path))); err != nil {
		return err
	}
	return g.maybePush(ctx)
}

func (g *Git) Head(ctx context.Context, scope string) (Version, error) {
	_ = ctx
	g.mu.Lock()
	defer g.mu.Unlock()
	scope, err := CleanPath(scope)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(g.headAbs(scope))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	return Version(strings.TrimSpace(string(raw))), nil
}

func (g *Git) SetHead(ctx context.Context, scope string, expected, next Version) error {
	_ = ctx
	g.mu.Lock()
	defer g.mu.Unlock()
	scope, err := CleanPath(scope)
	if err != nil {
		return err
	}
	abs := g.headAbs(scope)
	rel := g.headRel(scope)
	actual := Version("")
	if raw, err := os.ReadFile(abs); err == nil {
		actual = Version(strings.TrimSpace(string(raw)))
	} else if !os.IsNotExist(err) {
		return err
	}
	if actual != expected {
		return &ConflictError{Path: "head:" + scope, Expected: expected, Actual: actual}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, []byte(string(next)+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		return err
	}
	if err := g.commit([]string{rel}, fmt.Sprintf("contextverse: set-head %s", sanitizePath(scope))); err != nil {
		return err
	}
	return g.maybePush(ctx)
}

// Push pushes to the configured remote (no-op if unset).
func (g *Git) Push(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pushLocked(ctx)
}

// pushLocked is Push for callers already holding the lock. Separated rather than
// made reentrant: a mutex that is sometimes held twice is a deadlock waiting for
// the day somebody adds a call.
func (g *Git) pushLocked(ctx context.Context) error {
	if g.remote == "" {
		return nil
	}
	auth, err := g.auth.AuthMethod(g.remote)
	if err != nil {
		return err
	}
	err = g.repo.PushContext(ctx, &git.PushOptions{
		RemoteName: gitRemoteName,
		Auth:       auth,
	})
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	return err
}

// maybePush is only ever called from a locked method, so it must not re-lock.
func (g *Git) maybePush(ctx context.Context) error {
	if !g.autoPush {
		return nil
	}
	return g.pushLocked(ctx)
}

// TestConnectivity verifies the local repo opens and, if remote is set, that ls-remote works.
func (g *Git) TestConnectivity(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, err := g.repo.Head(); err != nil {
		return fmt.Errorf("local git head: %w", err)
	}
	if g.remote == "" {
		return nil
	}
	rem, err := g.repo.Remote(gitRemoteName)
	if err != nil {
		return err
	}
	auth, err := g.auth.AuthMethod(g.remote)
	if err != nil {
		return err
	}
	_, err = rem.ListContext(ctx, &git.ListOptions{Auth: auth})
	return err
}
