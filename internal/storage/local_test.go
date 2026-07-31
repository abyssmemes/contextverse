package storage

import (
	"bytes"
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

// A listing wants a path and a version, and the record keeps the file's bytes
// in the same JSON document. Decoding into the full record read and decoded
// every byte of every file to answer a question about names — on every tree,
// every changes and every quota check, holding the store's exclusive lock.
func TestListDoesNotDecodeFileBodies(t *testing.T) {
	l, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	body := bytes.Repeat([]byte("x"), 256<<10)
	for _, p := range []string{"a.md", "b.md", "team/c.md"} {
		if _, err := l.Put(ctx, p, body, ""); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := l.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("listed %d entries, want 3", len(entries))
	}
	for _, e := range entries {
		if e.Path == "" {
			t.Error("an entry came back with no path")
		}
		if e.Version == "" {
			t.Errorf("%s came back with no version; callers compare against it", e.Path)
		}
	}

	// The version a listing reports must be the one Get reports, or the two
	// disagree about what a caller is holding.
	_, ver, err := l.Get(ctx, "a.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Path == "a.md" && e.Version != ver {
			t.Errorf("list says %q, get says %q", e.Version, ver)
		}
	}
}

func TestListFiltersByPrefix(t *testing.T) {
	l, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, p := range []string{"team/a.md", "team/b.md", "other/c.md"} {
		if _, err := l.Put(ctx, p, []byte("body"), ""); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := l.List(ctx, "team/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("listed %d entries under team/, want 2", len(entries))
	}
}

// Quota accounting used to list the backend and then stat the working-tree
// mirror for sizes. That mirror is written by whichever replica handled the
// write, so with a shared backend every other replica counted the files as
// nothing and let the space grow past its limit. The backend has to answer.
func TestListReportsTheSizeOfEachObject(t *testing.T) {
	l, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	want := map[string]int64{}
	for _, tc := range []struct {
		path string
		size int
	}{{"a.md", 10}, {"b.md", 4096}, {"team/c.md", 1}} {
		body := bytes.Repeat([]byte("x"), tc.size)
		if _, err := l.Put(ctx, tc.path, body, ""); err != nil {
			t.Fatal(err)
		}
		want[tc.path] = int64(tc.size)
	}

	entries, err := l.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Fatalf("listed %d entries, want %d", len(entries), len(want))
	}
	for _, e := range entries {
		if e.Size != want[e.Path] {
			t.Errorf("%s reported %d bytes, want %d", e.Path, e.Size, want[e.Path])
		}
	}
}

// An empty file is legitimately zero bytes, and must not be mistaken for a size
// the backend failed to report.
func TestAnEmptyFileListsAsZero(t *testing.T) {
	l, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := l.Put(ctx, "empty.md", nil, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := l.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Size != 0 {
		t.Errorf("got %+v, want one entry of zero bytes", entries)
	}
}
