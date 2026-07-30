package storage

import (
	"context"
	"sync"
	"testing"
)

// The server opens a backend several times to serve one write. Before the pool
// each of those was a fresh connection — for Postgres a *sql.DB nothing ever
// closed, for S3 a HeadBucket round trip. These tests are about identity and
// lifetime, not about storage behaviour: what matters is that the second Open
// hands back the first one.

func localOpts(root, name string) OpenOptions {
	return OpenOptions{Driver: DriverLocal, SpaceRoot: root, SpaceName: name}
}

func TestPoolReusesTheBackendForOneSpace(t *testing.T) {
	p := NewPool()
	t.Cleanup(func() { _ = p.Close() })
	root := t.TempDir()

	first, err := p.Open(localOpts(root, "team"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Open(localOpts(root, "team"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("a second Open built a new backend; the per-request open is back")
	}
}

func TestPoolKeepsSpacesApart(t *testing.T) {
	p := NewPool()
	t.Cleanup(func() { _ = p.Close() })

	a, err := p.Open(localOpts(t.TempDir(), "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Open(localOpts(t.TempDir(), "beta"))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two spaces share one backend; one space's writes would land in the other")
	}
}

// A space that is deleted and recreated must not inherit the old handle — for
// the shared drivers that handle owns a connection to storage that is gone.
func TestPoolEvictsADeletedSpace(t *testing.T) {
	p := NewPool()
	t.Cleanup(func() { _ = p.Close() })
	root := t.TempDir()

	before, err := p.Open(localOpts(root, "team"))
	if err != nil {
		t.Fatal(err)
	}
	p.Evict("team")
	after, err := p.Open(localOpts(root, "team"))
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("Evict kept the old backend")
	}
}

// Eviction is by exact space name. A space called "team" must not take
// "team-archive" down with it.
func TestPoolEvictsOnlyTheNamedSpace(t *testing.T) {
	p := NewPool()
	t.Cleanup(func() { _ = p.Close() })
	root := t.TempDir()

	keep, err := p.Open(localOpts(root, "team-archive"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Open(localOpts(root, "team")); err != nil {
		t.Fatal(err)
	}
	p.Evict("team")

	again, err := p.Open(localOpts(root, "team-archive"))
	if err != nil {
		t.Fatal(err)
	}
	if keep != again {
		t.Error("evicting \"team\" also dropped \"team-archive\"")
	}
}

// Concurrent first-time opens must converge on one backend. Racing callers each
// keeping their own is the leak in a subtler form.
func TestPoolConvergesUnderConcurrentOpens(t *testing.T) {
	p := NewPool()
	t.Cleanup(func() { _ = p.Close() })
	root := t.TempDir()

	const n = 16
	got := make([]Backend, n)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, err := p.Open(localOpts(root, "team"))
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = b
		}(i)
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatalf("goroutine %d got a different backend; racing opens each kept their own", i)
		}
	}
}

// A failed open must not be remembered. A database that was down when the first
// request arrived would otherwise stay down for the life of the process.
func TestPoolDoesNotCacheAFailure(t *testing.T) {
	p := NewPool()
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.Open(OpenOptions{Driver: DriverLocal, SpaceRoot: "", SpaceName: "team"}); err == nil {
		t.Fatal("expected an empty space root to fail")
	}
	if _, err := p.Open(localOpts(t.TempDir(), "team")); err != nil {
		t.Fatalf("a later good open was refused: %v", err)
	}
}

// closeBackend must not reach through a Prefixed view. Those are per-space
// windows onto one shared pool, and closing through one would take storage away
// from every other space on the server.
func TestClosingAPrefixedViewLeavesTheSharedBackendAlone(t *testing.T) {
	inner := &closeCounter{}
	view := &Prefixed{Inner: inner, Prefix: "spaces/team"}

	if err := closeBackend(view); err != nil {
		t.Fatal(err)
	}
	if inner.closes != 0 {
		t.Errorf("closed the shared backend through a namespaced view (%d times)", inner.closes)
	}
	if err := closeBackend(inner); err != nil {
		t.Fatal(err)
	}
	if inner.closes != 1 {
		t.Errorf("closing the backend itself did not reach it: %d", inner.closes)
	}
}

type closeCounter struct {
	closes int
}

func (c *closeCounter) Name() string { return "counter" }
func (c *closeCounter) Close() error { c.closes++; return nil }
func (c *closeCounter) Get(context.Context, string) ([]byte, Version, error) {
	return nil, "", ErrNotFound
}
func (c *closeCounter) List(context.Context, string) ([]Entry, error) { return nil, nil }
func (c *closeCounter) Put(context.Context, string, []byte, Version) (Version, error) {
	return "", nil
}
func (c *closeCounter) Delete(context.Context, string, Version) error           { return nil }
func (c *closeCounter) Head(context.Context, string) (Version, error)           { return "", ErrNotFound }
func (c *closeCounter) SetHead(context.Context, string, Version, Version) error { return nil }
