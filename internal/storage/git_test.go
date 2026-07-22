package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGitPutGetCAS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := OpenGit(GitConfig{LocalPath: filepath.Join(dir, "git")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	v1, err := store.Put(ctx, "team/principles.md", []byte("p1"), "")
	if err != nil {
		t.Fatal(err)
	}
	data, ver, err := store.Get(ctx, "team/principles.md")
	if err != nil || string(data) != "p1" || ver != v1 {
		t.Fatalf("get: %q %q %v", data, ver, err)
	}
	_, err = store.Put(ctx, "team/principles.md", []byte("p2"), "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	if err := store.SetHead(ctx, SpaceScope, "", "snap1"); err != nil {
		t.Fatal(err)
	}
	h, err := store.Head(ctx, SpaceScope)
	if err != nil || h != "snap1" {
		t.Fatalf("head: %q %v", h, err)
	}
}

func TestHistorySnapshotRestoreMigrate(t *testing.T) {
	t.Parallel()
	space := t.TempDir()
	if err := os.MkdirAll(filepath.Join(space, "identity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(space, "identity", "me.md"), []byte("me-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(space, "config.yaml"), []byte("mode: solo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	local, err := OpenLocal(space)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hist := &History{Backend: local}

	meta, err := hist.SnapshotSpace(ctx, space, "first")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Files) != 1 {
		t.Fatalf("expected 1 file (config skipped), got %d: %+v", len(meta.Files), meta.Files)
	}

	if err := os.WriteFile(filepath.Join(space, "identity", "me.md"), []byte("me-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hist.RestoreSpace(ctx, space, meta.ID); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(space, "identity", "me.md"))
	if err != nil || string(got) != "me-v1" {
		t.Fatalf("restore: %q %v", got, err)
	}

	gitPath := filepath.Join(t.TempDir(), "git")
	gitStore, err := OpenGit(GitConfig{LocalPath: gitPath})
	if err != nil {
		t.Fatal(err)
	}
	n, err := Migrate(ctx, local, gitStore)
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 { // file + snapshot manifest
		t.Fatalf("migrate objects: %d", n)
	}
	data, _, err := gitStore.Get(ctx, "identity/me.md")
	if err != nil || string(data) != "me-v1" {
		t.Fatalf("git get: %q %v", data, err)
	}
}

func TestOpenFactory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := Open(OpenOptions{Driver: DriverLocal, SpaceRoot: dir})
	if err != nil || b.Name() != "local" {
		t.Fatalf("local: %v %v", b, err)
	}
	b, err = Open(OpenOptions{Driver: DriverGit, SpaceRoot: dir})
	if err != nil || b.Name() != "git" {
		t.Fatalf("git: %v %v", b, err)
	}
}
