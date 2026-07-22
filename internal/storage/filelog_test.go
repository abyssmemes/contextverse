package storage

import (
	"context"
	"errors"
	"testing"
)

func TestFileLogPutGetVersions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	fl := &FileLog{Backend: store, MaxVersions: 10}
	ctx := context.Background()

	v1, err := fl.Put(ctx, "notes/a.md", []byte("one"), "")
	if err != nil {
		t.Fatal(err)
	}
	if v1 != "1" {
		t.Fatalf("want v1, got %q", v1)
	}
	data, ver, err := fl.Get(ctx, "notes/a.md")
	if err != nil || string(data) != "one" || ver != "1" {
		t.Fatalf("get: %q %q %v", data, ver, err)
	}

	v2, err := fl.Put(ctx, "notes/a.md", []byte("two"), "1")
	if err != nil {
		t.Fatal(err)
	}
	if v2 != "2" {
		t.Fatalf("want v2, got %q", v2)
	}

	old, info, err := fl.GetVersion(ctx, "notes/a.md", 1)
	if err != nil || string(old) != "one" || info.Version != 1 {
		t.Fatalf("getversion: %q %+v %v", old, info, err)
	}

	_, err = fl.Put(ctx, "notes/a.md", []byte("stale"), "1")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	meta, list, err := fl.ListVersions(ctx, "notes/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Current != 2 || len(list) != 2 || list[0].Version != 2 {
		t.Fatalf("list: current=%d list=%+v", meta.Current, list)
	}
}

func TestFileLogSoftDeleteUndelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	fl := &FileLog{Backend: store}
	ctx := context.Background()

	_, _ = fl.Put(ctx, "x.md", []byte("live"), "")
	if err := fl.SoftDelete(ctx, "x.md", "1"); err != nil {
		t.Fatal(err)
	}
	_, _, err = fl.Get(ctx, "x.md")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found after soft-delete, got %v", err)
	}
	ver, err := fl.Undelete(ctx, "x.md")
	if err != nil || ver != "1" {
		t.Fatalf("undelete: %q %v", ver, err)
	}
	data, _, err := fl.Get(ctx, "x.md")
	if err != nil || string(data) != "live" {
		t.Fatalf("after undelete: %q %v", data, err)
	}
}

func TestFileLogAdoptLegacy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hashVer, err := store.Put(ctx, "legacy.md", []byte("old"), "")
	if err != nil {
		t.Fatal(err)
	}

	fl := &FileLog{Backend: store}
	data, ver, err := fl.Get(ctx, "legacy.md")
	if err != nil || string(data) != "old" || ver != "1" {
		t.Fatalf("adopt get: %q %q %v", data, ver, err)
	}

	// Legacy content-hash If-Match still works once.
	v2, err := fl.Put(ctx, "legacy.md", []byte("new"), hashVer)
	if err != nil {
		t.Fatalf("legacy cas put: %v", err)
	}
	if v2 != "2" {
		t.Fatalf("want v2, got %q", v2)
	}
}

func TestFileLogPrune(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	fl := &FileLog{Backend: store, MaxVersions: 3}
	ctx := context.Background()

	cur := Version("")
	for i := 1; i <= 5; i++ {
		next, err := fl.Put(ctx, "p.md", []byte{byte('0' + i)}, cur)
		if err != nil {
			t.Fatal(err)
		}
		cur = next
	}
	_, list, err := fl.ListVersions(ctx, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	alive := 0
	for _, v := range list {
		if !v.Destroyed {
			alive++
		}
	}
	if alive != 3 {
		t.Fatalf("expected 3 live versions, got %d (%+v)", alive, list)
	}
	_, _, err = fl.GetVersion(ctx, "p.md", 1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("v1 should be pruned, got %v", err)
	}
}
