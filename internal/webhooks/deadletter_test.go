package webhooks

import (
	"os"
	"strings"
	"testing"
	"time"
)

func fill(t *testing.T, s *Store, n int, note string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.appendDeadLetter(DeadLetter{
			HookID:   "wh_1",
			URL:      "https://example.com/hook",
			Error:    note,
			FailedAt: time.Now().UTC(),
			Attempts: 3,
			Event:    Event{Type: "space.push", Space: "team", Data: map[string]any{"pad": strings.Repeat("x", 512)}},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

func TestDeadLetterRotatesInsteadOfGrowing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Each record is well under a kilobyte, so this comfortably crosses the cap.
	fill(t, s, 20000, "endpoint down")

	fi, err := os.Stat(s.deadLetterPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > maxDeadLetterBytes {
		t.Fatalf("live dead-letter file grew to %d bytes, past the %d cap", fi.Size(), maxDeadLetterBytes)
	}
	if _, err := os.Stat(s.rotatedDeadLetterPath()); err != nil {
		t.Fatalf("expected a rotated file: %v", err)
	}
}

func TestDeadLetterListingStaysBounded(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s, 5000, "endpoint down")

	rows, err := s.ListDeadLetter(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 50 {
		t.Fatalf("asked for 50 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.HookID != "wh_1" {
			t.Fatalf("tail read produced a malformed row: %#v", r)
		}
	}
}

func TestDeadLetterListingReachesIntoTheRotatedFile(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s, 20000, "old")
	// Just a couple of fresh rows after the rotation.
	fill(t, s, 2, "new")

	rows, err := s.ListDeadLetter(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 3 {
		t.Fatalf("rotation should not hide recent history, got %d rows", len(rows))
	}
	if rows[len(rows)-1].Error != "new" {
		t.Fatalf("newest row should be last, got %q", rows[len(rows)-1].Error)
	}
}

func TestListingMissingFileIsEmpty(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListDeadLetter(10)
	if err != nil {
		t.Fatalf("a store with no failures must not error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows from an empty store", len(rows))
	}
}
