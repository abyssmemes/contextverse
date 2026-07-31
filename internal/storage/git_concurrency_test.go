package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// Local took a flock around every operation. This backend took nothing, while
// implementing the same interface — one that promises compare-and-swap. Two
// separate failures follow from that: the compare and the swap were not one
// operation, so a lost update was possible; and go-git's worktree shares one
// index between Add and Commit, so overlapping commits corrupt it.
//
// Run with -race these would be flaky rather than reliably wrong, which is the
// worst way for a storage bug to present. What is asserted here is the outcome:
// exactly one winner per contended write, and a repository still readable
// afterwards.

func gitBackend(t *testing.T) *Git {
	t.Helper()
	g, err := OpenGit(GitConfig{LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// Every writer starts from the same version. CAS means one of them wins.
func TestGitConcurrentCreatesProduceOneWinner(t *testing.T) {
	g := gitBackend(t)
	ctx := context.Background()

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Empty expected version means "create": only one may.
			_, errs[i] = g.Put(ctx, "notes.md", []byte(fmt.Sprintf("writer %d", i)), "")
		}(i)
	}
	wg.Wait()

	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrConflict):
		default:
			t.Errorf("writer %d failed with %v, want nil or a conflict", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d writers created the same path, want exactly 1", won)
	}
}

// A read-modify-write cycle run concurrently must not lose an update: every
// successful Put has to have been against the version it actually replaced.
func TestGitConcurrentOverwritesDoNotLoseUpdates(t *testing.T) {
	g := gitBackend(t)
	ctx := context.Background()

	base, err := g.Put(ctx, "notes.md", []byte("base"), "")
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = g.Put(ctx, "notes.md", []byte(fmt.Sprintf("update %d", i)), base)
		}(i)
	}
	wg.Wait()

	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrConflict):
		default:
			t.Errorf("writer %d failed with %v", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d writers succeeded against one version, want exactly 1", won)
	}

	// And the survivor is readable, which is what a corrupt index would cost.
	data, ver, err := g.Get(ctx, "notes.md")
	if err != nil {
		t.Fatalf("the file is unreadable after concurrent writes: %v", err)
	}
	if ver == base {
		t.Error("the version never moved; no write landed")
	}
	if len(data) == 0 {
		t.Error("the file is empty")
	}
}

// Heads carry the same CAS promise, through a different file and the same index.
func TestGitConcurrentHeadUpdatesProduceOneWinner(t *testing.T) {
	g := gitBackend(t)
	ctx := context.Background()

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = g.SetHead(ctx, SpaceScope, "", Version(fmt.Sprintf("head-%d", i)))
		}(i)
	}
	wg.Wait()

	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d writers set the head from empty, want exactly 1", won)
	}
	if _, err := g.Head(ctx, SpaceScope); err != nil {
		t.Fatalf("the head is unreadable afterwards: %v", err)
	}
}

// Writes to different paths still share one index and one HEAD, so they are the
// case most likely to corrupt the repository rather than merely lose a write.
func TestGitConcurrentWritesToDifferentPathsAllLand(t *testing.T) {
	g := gitBackend(t)
	ctx := context.Background()

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := g.Put(ctx, fmt.Sprintf("f%d.md", i), []byte("body"), ""); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	entries, err := g.List(ctx, "")
	if err != nil {
		t.Fatalf("listing after concurrent writes: %v", err)
	}
	if len(entries) != n {
		t.Errorf("%d files present, want %d — a write was lost", len(entries), n)
	}
	for i := 0; i < n; i++ {
		if _, _, err := g.Get(ctx, fmt.Sprintf("f%d.md", i)); err != nil {
			t.Errorf("f%d.md unreadable: %v", i, err)
		}
	}
}
