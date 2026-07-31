package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A versioned write is three objects — the version blob, the metadata, the live
// mirror — and no backend can write three things at once. What can be arranged
// is that every way of stopping part-way leaves a state somebody can explain.
//
// It could not, before. The order was version blob, live, metadata: stop after
// the second and the live file holds the new content while the metadata still
// calls the old version current, so a read returned the new bytes labelled with
// the old version and every CAS token handed out was a lie about content the
// caller had never seen.

// failingBackend fails the nth write to a matching path, so a test can stop a
// three-step write anywhere in the middle.
type failingBackend struct {
	Backend
	failOn func(path string) bool
}

var errInjected = errors.New("injected write failure")

func (b *failingBackend) Put(ctx context.Context, path string, data []byte, expected Version) (Version, error) {
	if b.failOn != nil && b.failOn(path) {
		return "", errInjected
	}
	return b.Backend.Put(ctx, path, data, expected)
}

func logOver(t *testing.T, failOn func(string) bool) *FileLog {
	t.Helper()
	local, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &FileLog{Backend: &failingBackend{Backend: local, failOn: failOn}}
}

// The read must come from the version the metadata names, whatever the mirror
// happens to hold.
func TestGetReturnsTheVersionItReports(t *testing.T) {
	fl := logOver(t, nil)
	ctx := context.Background()

	if _, err := fl.Put(ctx, "notes.md", []byte("first"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fl.Put(ctx, "notes.md", []byte("second"), FormatFileVersion(1)); err != nil {
		t.Fatal(err)
	}

	data, ver, err := fl.Get(ctx, "notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" || ver != FormatFileVersion(2) {
		t.Fatalf("got %q at %q, want \"second\" at v2", data, ver)
	}
}

// Interrupted before the metadata: nothing was published, so the previous
// version is still current and still readable. The orphaned blob costs storage
// and nothing else.
func TestAWriteInterruptedBeforeTheCommitChangesNothing(t *testing.T) {
	fl := logOver(t, func(p string) bool { return strings.HasPrefix(p, FileMetaPrefix) })
	ctx := context.Background()

	// The first write needs metadata, so allow it, then arm the failure.
	fb := fl.Backend.(*failingBackend)
	fb.failOn = nil
	if _, err := fl.Put(ctx, "notes.md", []byte("first"), ""); err != nil {
		t.Fatal(err)
	}
	fb.failOn = func(p string) bool { return strings.HasPrefix(p, FileMetaPrefix) }

	if _, err := fl.Put(ctx, "notes.md", []byte("second"), FormatFileVersion(1)); !errors.Is(err, errInjected) {
		t.Fatalf("expected the injected failure, got %v", err)
	}

	data, ver, err := fl.Get(ctx, "notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first" || ver != FormatFileVersion(1) {
		t.Errorf("after a failed write the file reads %q at %q, want \"first\" at v1", data, ver)
	}
}

// Interrupted after the metadata but before the mirror: the version is real and
// reads correctly. Only the listing mirror is briefly behind, which costs
// nobody a wrong answer.
func TestAWriteInterruptedAfterTheCommitStillReadsCorrectly(t *testing.T) {
	local, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := &failingBackend{Backend: local}
	fl := &FileLog{Backend: fb}
	ctx := context.Background()

	if _, err := fl.Put(ctx, "notes.md", []byte("first"), ""); err != nil {
		t.Fatal(err)
	}

	// Fail only the live mirror: not the version blob, not the metadata.
	fb.failOn = func(p string) bool {
		return !strings.HasPrefix(p, FileMetaPrefix) && !strings.HasPrefix(p, FileVerPrefix)
	}
	_, err = fl.Put(ctx, "notes.md", []byte("second"), FormatFileVersion(1))
	if err == nil {
		t.Fatal("a failed mirror write reported success")
	}
	if !strings.Contains(err.Error(), "recorded") {
		t.Errorf("the error does not say the version was committed: %v", err)
	}

	// The committed version is what a reader gets — not the stale mirror.
	fb.failOn = nil
	data, ver, err := fl.Get(ctx, "notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" || ver != FormatFileVersion(2) {
		t.Errorf("read %q at %q, want the committed \"second\" at v2", data, ver)
	}
}

// A version the metadata names whose content is gone must be an error, not a
// quiet fallback to whatever the mirror holds — that would hand back content
// belonging to a different version than the one reported.
func TestAMissingVersionBlobIsAnError(t *testing.T) {
	local, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fl := &FileLog{Backend: local}
	ctx := context.Background()

	if _, err := fl.Put(ctx, "notes.md", []byte("only"), ""); err != nil {
		t.Fatal(err)
	}
	// Remove the version blob behind the log's back.
	_, ver, err := local.Get(ctx, verBlobPath("notes.md", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Delete(ctx, verBlobPath("notes.md", 1), ver); err != nil {
		t.Fatal(err)
	}

	if _, _, err := fl.Get(ctx, "notes.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want a not-found rather than the mirror's copy", err)
	}
}

// The ordinary sequence still has to work, or atomicity has been bought by
// breaking the feature.
func TestVersionsStillAccumulate(t *testing.T) {
	fl := logOver(t, nil)
	ctx := context.Background()

	expected := Version("")
	for i, body := range []string{"one", "two", "three"} {
		ver, err := fl.Put(ctx, "notes.md", []byte(body), expected)
		if err != nil {
			t.Fatalf("write %d: %v", i+1, err)
		}
		expected = ver
	}
	_, versions, err := fl.ListVersions(ctx, "notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("kept %d versions, want 3", len(versions))
	}
	for _, want := range []struct {
		n    int
		body string
	}{{1, "one"}, {2, "two"}, {3, "three"}} {
		data, _, err := fl.GetVersion(ctx, "notes.md", want.n)
		if err != nil {
			t.Errorf("v%d: %v", want.n, err)
			continue
		}
		if string(data) != want.body {
			t.Errorf("v%d = %q, want %q", want.n, data, want.body)
		}
	}
}
