package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewDisabled(t *testing.T) {
	p, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Enabled() {
		t.Fatal("empty endpoint should disable tracing")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := p.Middleware(func(context.Context) string { return "rid" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestNormalizeEndpointURL(t *testing.T) {
	if got := normalizeEndpointURL("localhost:4318"); got != "http://localhost:4318" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeEndpointURL("http://collector:4318/"); got != "http://collector:4318" {
		t.Fatalf("got %q", got)
	}
}
