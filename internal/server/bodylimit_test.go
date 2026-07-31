package server

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// io.LimitReader stops at the cap and io.ReadAll calls that success, so a body
// that had more to give was indistinguishable from one that ended. handlePutFile
// read through a 32 MiB limit reader and stored whatever came back: upload 40
// MiB and the server answered 200 with a file two thirds of the way through a
// sentence. Silent truncation on a write path is the worst kind of bug — the
// client is told its document is safe.

func TestABodyAtTheLimitIsAccepted(t *testing.T) {
	body := strings.Repeat("x", 100)
	got, err := readAtMost(bytes.NewBufferString(body), 100)
	if err != nil {
		t.Fatalf("a body exactly at the limit was refused: %v", err)
	}
	if string(got) != body {
		t.Errorf("read %d bytes, want %d", len(got), len(body))
	}
}

func TestABodyOverTheLimitIsRefusedNotTruncated(t *testing.T) {
	body := strings.Repeat("x", 101)
	got, err := readAtMost(bytes.NewBufferString(body), 100)
	if !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("got (%d bytes, %v), want a refusal", len(got), err)
	}
	if got != nil {
		t.Errorf("returned %d bytes alongside the error; a caller could store them", len(got))
	}
}

// One byte over is the case that matters: it is the smallest overrun, and the
// one a naive limit reader is least likely to notice.
func TestOneByteOverIsStillRefused(t *testing.T) {
	for _, limit := range []int64{1, 2, 1024, 5 << 20} {
		body := strings.Repeat("y", int(limit)+1)
		if _, err := readAtMost(bytes.NewBufferString(body), limit); !errors.Is(err, errBodyTooLarge) {
			t.Errorf("limit %d: %d bytes accepted, want a refusal", limit, len(body))
		}
	}
}

// A limit of zero means "unset", not "accept nothing" — a space with no
// configured maximum must still be able to take an ordinary file.
func TestAnUnsetLimitFallsBackToADefault(t *testing.T) {
	if _, err := readAtMost(bytes.NewBufferString("small"), 0); err != nil {
		t.Fatalf("an unset limit refused a small body: %v", err)
	}
}

func TestAnEmptyBodyIsFine(t *testing.T) {
	got, err := readAtMost(bytes.NewBuffer(nil), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("read %d bytes from an empty body", len(got))
	}
}
