package storage

import (
	"context"
	"testing"
)

type memBackend struct {
	blobs map[string]struct {
		data []byte
		ver  Version
	}
	heads map[string]Version
}

func newMem() *memBackend {
	return &memBackend{
		blobs: map[string]struct {
			data []byte
			ver  Version
		}{},
		heads: map[string]Version{},
	}
}

func (m *memBackend) Name() string { return "mem" }

func (m *memBackend) Get(_ context.Context, path string) ([]byte, Version, error) {
	b, ok := m.blobs[path]
	if !ok {
		return nil, "", ErrNotFound
	}
	return append([]byte(nil), b.data...), b.ver, nil
}

func (m *memBackend) List(_ context.Context, prefix string) ([]Entry, error) {
	var out []Entry
	for p, b := range m.blobs {
		if stringsHasPrefix(p, prefix) {
			out = append(out, Entry{Path: p, Version: b.ver})
		}
	}
	return out, nil
}

func stringsHasPrefix(s, p string) bool {
	if p == "" {
		return true
	}
	return len(s) >= len(p) && s[:len(p)] == p
}

func (m *memBackend) Put(_ context.Context, path string, data []byte, expected Version) (Version, error) {
	cur, ok := m.blobs[path]
	if ok {
		if cur.ver != expected {
			return "", ErrConflict
		}
	} else if expected != "" {
		return "", ErrConflict
	}
	next := Version("v1")
	if ok {
		next = Version(string(cur.ver) + "x")
	}
	m.blobs[path] = struct {
		data []byte
		ver  Version
	}{append([]byte(nil), data...), next}
	return next, nil
}

func (m *memBackend) Delete(_ context.Context, path string, expected Version) error {
	cur, ok := m.blobs[path]
	if !ok {
		return ErrNotFound
	}
	if cur.ver != expected {
		return ErrConflict
	}
	delete(m.blobs, path)
	return nil
}

func (m *memBackend) Head(_ context.Context, scope string) (Version, error) {
	h, ok := m.heads[scope]
	if !ok {
		return "", ErrNotFound
	}
	return h, nil
}

func (m *memBackend) SetHead(_ context.Context, scope string, expected, next Version) error {
	cur, ok := m.heads[scope]
	if ok {
		if cur != expected {
			return ErrConflict
		}
	} else if expected != "" {
		return ErrConflict
	}
	m.heads[scope] = next
	return nil
}

func TestPrefixedIsolation(t *testing.T) {
	inner := newMem()
	a := &Prefixed{Inner: inner, Prefix: "spaces/a"}
	b := &Prefixed{Inner: inner, Prefix: "spaces/b"}
	ctx := context.Background()

	va, err := a.Put(ctx, "file.md", []byte("A"), "")
	if err != nil {
		t.Fatal(err)
	}
	vb, err := b.Put(ctx, "file.md", []byte("B"), "")
	if err != nil {
		t.Fatal(err)
	}
	dataA, _, err := a.Get(ctx, "file.md")
	if err != nil || string(dataA) != "A" {
		t.Fatalf("a: %q %v", dataA, err)
	}
	dataB, _, err := b.Get(ctx, "file.md")
	if err != nil || string(dataB) != "B" {
		t.Fatalf("b: %q %v", dataB, err)
	}
	if err := a.SetHead(ctx, SpaceScope, "", "ha"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetHead(ctx, SpaceScope, "", "hb"); err != nil {
		t.Fatal(err)
	}
	ha, _ := a.Head(ctx, SpaceScope)
	hb, _ := b.Head(ctx, SpaceScope)
	if ha != "ha" || hb != "hb" {
		t.Fatalf("heads %q %q", ha, hb)
	}
	_ = va
	_ = vb
}
