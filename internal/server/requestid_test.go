package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The request id goes into structured logs and into the error envelope, and it
// came from a header the caller controls with nothing checked. A newline forges
// a log line; control bytes confuse whatever reads them; an unbounded string is
// an unbounded log record.

func TestAWellFormedRequestIDIsKept(t *testing.T) {
	for _, id := range []string{
		"abc123",
		"trace-1234",
		"a.b.c",
		"span:0af7651916cd43dd",
		"UPPER_case-123",
	} {
		if got := sanitizeRequestID(id); got != id {
			t.Errorf("sanitizeRequestID(%q) = %q; a legitimate id was discarded", id, got)
		}
	}
}

func TestADangerousRequestIDIsDiscarded(t *testing.T) {
	for _, id := range []string{
		"line\nINFO forged=\"log entry\"", // the whole point
		"tab\there",
		"null\x00byte",
		"esc\x1b[31m",
		"spaces here",
		`quote"and'`,
		"unicode-Привет",
		strings.Repeat("x", maxRequestIDLen+1),
	} {
		if got := sanitizeRequestID(id); got != "" {
			t.Errorf("sanitizeRequestID(%q) kept %q", id, got)
		}
	}
}

// A discarded id must be replaced, not left empty: every request needs one.
func TestAForgedIDIsReplacedNotEchoed(t *testing.T) {
	s := &Server{}
	var seen string
	h := s.withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = r.Context().Value(requestIDKey).(string)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("X-Request-Id", "evil\nINFO level=forged")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seen == "" {
		t.Fatal("no request id was assigned")
	}
	if strings.ContainsAny(seen, "\n\r") {
		t.Errorf("the forged id reached the handler: %q", seen)
	}
	if echoed := rec.Header().Get("X-Request-Id"); echoed != seen {
		t.Errorf("header says %q, context says %q", echoed, seen)
	}
}

// Generated ids are random. The old ones were UnixNano: guessable, so a caller
// could predict somebody else's, and identical for two requests in the same
// tick on a platform with a coarse clock — exactly when telling them apart
// matters.
func TestGeneratedIDsAreUniqueAndNotAClock(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := randomRequestID()
		if seen[id] {
			t.Fatalf("duplicate generated id %q after %d", id, i)
		}
		seen[id] = true
		if sanitizeRequestID(id) != id {
			t.Fatalf("a generated id %q does not survive its own validation", id)
		}
	}
}

// The rate limiter keeps its buckets for the life of the process. A live bearer
// token in that map is a working credential sitting in memory long after the
// request that carried it.
func TestTheRateLimitKeyDoesNotCarryTheToken(t *testing.T) {
	s := &Server{}
	const token = "cv-kim-supersecret"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/spaces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "203.0.113.7:4242"

	key := s.rateLimitKey(req)
	if strings.Contains(key, token) {
		t.Fatalf("the key holds the token: %q", key)
	}
	if !strings.HasPrefix(key, "bearer:") {
		t.Errorf("key = %q; token callers should still be counted per token", key)
	}

	// Same token, same bucket: hashing must not break the limiting.
	if again := s.rateLimitKey(req); again != key {
		t.Error("the same token produced two different keys")
	}

	other := httptest.NewRequest(http.MethodGet, "/api/v1/spaces", nil)
	other.Header.Set("Authorization", "Bearer cv-sam-different")
	if s.rateLimitKey(other) == key {
		t.Error("two tokens share a bucket")
	}
}
