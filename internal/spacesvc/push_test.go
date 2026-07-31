package spacesvc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/quotas"
	"github.com/orkcom-tech/contextverse/internal/storage"
)

// "Push applies ops transactionally" was a comment, not a property. These are
// the property: a batch that fails changes nothing, a batch nobody has
// committed is invisible to a puller, and two batches at once do not interleave.

func pushService(t *testing.T, q quotas.Config) (*Service, string) {
	t.Helper()
	svc := &Service{DataDir: t.TempDir(), Quotas: q}
	t.Cleanup(func() { _ = svc.Close() })
	if _, err := svc.Create(context.Background(), "team", "solo-default", true); err != nil {
		t.Fatal(err)
	}
	return svc, "team"
}

func put(path, body string) PushOp {
	return PushOp{Op: "put", Path: path, ContentB64: base64.StdEncoding.EncodeToString([]byte(body))}
}

func headOf(t *testing.T, svc *Service, space string) string {
	t.Helper()
	h, err := svc.Head(context.Background(), space)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	return string(h)
}

func mustPush(t *testing.T, svc *Service, space string, ops ...PushOp) *PushResult {
	t.Helper()
	res, err := svc.Push(context.Background(), space, PushRequest{ExpectedHead: headOf(t, svc, space), Ops: ops})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	return res
}

func fileBody(t *testing.T, svc *Service, space, path string) (string, bool) {
	t.Helper()
	data, _, err := svc.GetFile(context.Background(), space, path)
	if errors.Is(err, storage.ErrNotFound) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

// spaceWithHeadroom creates a space and then sets the quota relative to what the
// template already occupies. Absolute byte limits in these tests would be
// testing the size of the seed rather than the batch.
func spaceWithHeadroom(t *testing.T, headroom int64) (*Service, string) {
	t.Helper()
	svc, space := pushService(t, quotas.Default())
	_, baseline, err := svc.inventory(context.Background(), space)
	if err != nil {
		t.Fatal(err)
	}
	svc.Quotas = quotas.Config{MaxFileSize: 1 << 20, MaxSpaceSize: baseline + headroom, MaxFiles: 1000}
	return svc, space
}

func TestPushAppliesAWholeBatch(t *testing.T) {
	svc, space := pushService(t, quotas.Default())

	res := mustPush(t, svc, space, put("a.md", "one"), put("b.md", "two"))
	if res.Applied != 2 {
		t.Fatalf("applied %d, want 2", res.Applied)
	}
	for path, want := range map[string]string{"a.md": "one", "b.md": "two"} {
		if got, ok := fileBody(t, svc, space, path); !ok || got != want {
			t.Errorf("%s = %q (present=%v), want %q", path, got, ok, want)
		}
	}
}

// The one that mattered: a batch rejected part-way must leave nothing behind.
// Before, the operations before the bad one were already in storage and in the
// working tree, and head had not moved — so a puller never learned about them.
func TestARejectedBatchWritesNothing(t *testing.T) {
	svc, space := pushService(t, quotas.Default())
	before := headOf(t, svc, space)

	_, err := svc.Push(context.Background(), space, PushRequest{
		ExpectedHead: before,
		Ops: []PushOp{
			put("first.md", "written"),
			put("second.md", "written"),
			{Op: "explode", Path: "third.md"}, // rejected
		},
	})
	if err == nil {
		t.Fatal("an unknown operation was accepted")
	}

	for _, path := range []string{"first.md", "second.md"} {
		if body, ok := fileBody(t, svc, space, path); ok {
			t.Errorf("%s survived a failed batch with %q", path, body)
		}
		abs, perr := svc.treePath(space, path)
		if perr != nil {
			t.Fatal(perr)
		}
		if _, serr := os.Stat(abs); serr == nil {
			t.Errorf("%s was left in the working tree", path)
		}
	}
	if after := headOf(t, svc, space); after != before {
		t.Errorf("head moved from %q to %q on a failed push", before, after)
	}
}

// A batch that breaches the quota is refused before it writes, not while it is
// writing — the check now runs over the batch's end state.
func TestAnOversizedBatchIsRefusedBeforeItWrites(t *testing.T) {
	svc, space := spaceWithHeadroom(t, 1000)

	big := strings.Repeat("x", 900)
	_, err := svc.Push(context.Background(), space, PushRequest{
		ExpectedHead: headOf(t, svc, space),
		Ops:          []PushOp{put("one.md", big), put("two.md", big), put("three.md", big)},
	})
	var qe *quotas.Exceeded
	if !errors.As(err, &qe) {
		t.Fatalf("expected a quota refusal, got %v", err)
	}
	for _, path := range []string{"one.md", "two.md", "three.md"} {
		if _, ok := fileBody(t, svc, space, path); ok {
			t.Errorf("%s was written by a batch that did not fit", path)
		}
	}
}

// A batch is judged on its net effect: a push that only fits because it also
// deletes something must be allowed.
func TestABatchIsJudgedOnItsNetEffect(t *testing.T) {
	svc, space := spaceWithHeadroom(t, 2000)

	filler := strings.Repeat("x", 1500)
	mustPush(t, svc, space, put("old.md", filler))

	// The put comes first on purpose: applied one operation at a time this batch
	// passes through a state that does not fit, and the old accounting refused it
	// for that reason alone. What matters is where the space ends up.
	res := mustPush(t, svc, space,
		put("new.md", filler),
		PushOp{Op: "delete", Path: "old.md"},
	)
	if res.Applied != 2 {
		t.Fatalf("applied %d, want 2", res.Applied)
	}
	if _, ok := fileBody(t, svc, space, "old.md"); ok {
		t.Error("old.md is still live")
	}
	if body, ok := fileBody(t, svc, space, "new.md"); !ok || body != filler {
		t.Error("new.md was not written")
	}
}

// blockWorkingTreePath makes one path impossible to write by putting a
// directory where the file has to go. It is the simplest way to fail during
// apply rather than during planning: everything a plan can check now passes,
// so the remaining failures are the I/O ones, and those are what rollback is
// for.
func blockWorkingTreePath(t *testing.T, svc *Service, space, path string) {
	t.Helper()
	abs, err := svc.treePath(space, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
}

// Overwrites roll back to the previous content, not merely to absent.
func TestAFailedBatchRestoresPreviousContent(t *testing.T) {
	svc, space := pushService(t, quotas.Default())
	mustPush(t, svc, space, put("notes.md", "original"))
	blockWorkingTreePath(t, svc, space, "unwritable.md")

	_, err := svc.Push(context.Background(), space, PushRequest{
		ExpectedHead: headOf(t, svc, space),
		Ops: []PushOp{
			put("notes.md", "replacement"),
			put("unwritable.md", "never lands"),
		},
	})
	if err == nil {
		t.Fatal("the batch was accepted")
	}
	if body, ok := fileBody(t, svc, space, "notes.md"); !ok || body != "original" {
		t.Errorf("notes.md = %q (present=%v), want the original content back", body, ok)
	}
	abs, _ := svc.treePath(space, "notes.md")
	raw, rerr := os.ReadFile(abs)
	if rerr != nil {
		t.Fatalf("the working-tree copy is gone: %v", rerr)
	}
	if string(raw) != "original" {
		t.Errorf("working tree holds %q, want the original", raw)
	}
}

// A deletion that is rolled back brings the file back.
func TestAFailedBatchRestoresADeletedFile(t *testing.T) {
	svc, space := pushService(t, quotas.Default())
	mustPush(t, svc, space, put("keep.md", "content"))

	blockWorkingTreePath(t, svc, space, "unwritable.md")

	_, err := svc.Push(context.Background(), space, PushRequest{
		ExpectedHead: headOf(t, svc, space),
		Ops: []PushOp{
			{Op: "delete", Path: "keep.md"},
			put("unwritable.md", "never lands"),
		},
	})
	if err == nil {
		t.Fatal("the batch was accepted")
	}
	if body, ok := fileBody(t, svc, space, "keep.md"); !ok || body != "content" {
		t.Errorf("keep.md = %q (present=%v), want it restored", body, ok)
	}
}

// Two pushes at once must not interleave. Both read the same head, so exactly
// one may commit; the loser must be told, and must not have written anything.
func TestConcurrentPushesDoNotInterleave(t *testing.T) {
	svc, space := pushService(t, quotas.Default())
	start := headOf(t, svc, space)

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.Push(context.Background(), space, PushRequest{
				ExpectedHead: start,
				Ops:          []PushOp{put(fmt.Sprintf("w%d.md", i), fmt.Sprintf("body %d", i))},
			})
		}(i)
	}
	wg.Wait()

	won := 0
	for i, err := range errs {
		if err == nil {
			won++
			continue
		}
		if !errors.Is(err, storage.ErrConflict) {
			t.Errorf("push %d failed with %v, want a version conflict", i, err)
		}
		// A loser must not have left its file behind.
		if body, ok := fileBody(t, svc, space, fmt.Sprintf("w%d.md", i)); ok {
			t.Errorf("push %d lost the race but wrote %q", i, body)
		}
	}
	if won != 1 {
		t.Fatalf("%d pushes committed against one head, want exactly 1", won)
	}
}

// The inventory must not fail open. A space whose contents cannot be listed used
// to pass the quota check outright — "don't block on list failure" — which turned
// a broken space into an unlimited one.
func TestQuotaCheckFailsClosedWhenTheSpaceCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{DataDir: dir, Quotas: quotas.Default()}
	t.Cleanup(func() { _ = svc.Close() })

	// A file where the space's directory should be: the backend cannot open, so
	// listing cannot answer. Any unreadable state would do; this one needs no
	// permission games that a test running as root would defeat.
	if err := os.MkdirAll(filepath.Join(dir, "spaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spaces", "broken"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := svc.inventory(context.Background(), "broken"); err == nil {
		t.Fatal("an unreadable space reported an inventory instead of an error")
	}

	// And the write path must refuse rather than wave the write through.
	if _, err := svc.PutFile(context.Background(), "broken", "notes.md", []byte("hello"), ""); err == nil {
		t.Fatal("a write to an unreadable space was accepted; the quota check failed open")
	}
}

// A stale version marker is the ordinary reason a push is refused, and it must
// be found before anything is written rather than half way through the batch.
func TestAStaleVersionIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	svc, space := pushService(t, quotas.Default())
	mustPush(t, svc, space, put("notes.md", "original"))

	first := put("fresh.md", "written")
	stale := put("notes.md", "replacement")
	stale.Expected = "99" // nothing is at version 99

	_, err := svc.Push(context.Background(), space, PushRequest{
		ExpectedHead: headOf(t, svc, space),
		Ops:          []PushOp{first, stale},
	})
	if !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("got %v, want a version conflict", err)
	}
	if body, ok := fileBody(t, svc, space, "fresh.md"); ok {
		t.Errorf("fresh.md was written by a batch that was refused: %q", body)
	}
	if body, _ := fileBody(t, svc, space, "notes.md"); body != "original" {
		t.Errorf("notes.md = %q, want it untouched", body)
	}
}
