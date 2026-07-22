package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestLocalPutGetCAS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	v1, err := store.Put(ctx, "identity/me.md", []byte("hello"), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v1 == "" {
		t.Fatal("empty version")
	}

	data, ver, err := store.Get(ctx, "identity/me.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" || ver != v1 {
		t.Fatalf("get mismatch: %q %q", data, ver)
	}

	_, err = store.Put(ctx, "identity/me.md", []byte("stale"), "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	v2, err := store.Put(ctx, "identity/me.md", []byte("world"), v1)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if v2 == v1 {
		t.Fatal("version did not change")
	}

	if err := store.Delete(ctx, "identity/me.md", v1); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete expected conflict, got %v", err)
	}
	if err := store.Delete(ctx, "identity/me.md", v2); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Get(ctx, "identity/me.md")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestLocalListAndHead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	_, _ = store.Put(ctx, "a/one.md", []byte("1"), "")
	_, _ = store.Put(ctx, "a/two.md", []byte("2"), "")
	_, _ = store.Put(ctx, "b/three.md", []byte("3"), "")

	entries, err := store.List(ctx, "a/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("list a/: got %d", len(entries))
	}

	_, err = store.Head(ctx, "space")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("head expected not found: %v", err)
	}
	if err := store.SetHead(ctx, "space", "", "v1"); err != nil {
		t.Fatal(err)
	}
	h, err := store.Head(ctx, "space")
	if err != nil || h != "v1" {
		t.Fatalf("head: %q %v", h, err)
	}
	if err := store.SetHead(ctx, "space", "", "v2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("sethead expected conflict: %v", err)
	}
	if err := store.SetHead(ctx, "space", "v1", "v2"); err != nil {
		t.Fatal(err)
	}
}

func TestLocalMetaDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".contextverse")
	if store.Root() != want {
		t.Fatalf("root: got %s want %s", store.Root(), want)
	}
}

func TestSanitizePath(t *testing.T) {
	t.Parallel()
	if got := sanitizePath("/a/../b//c.md"); got != "b/c.md" {
		t.Fatalf("got %q", got)
	}
}
