package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/config"
)

// ListenAndServe reaches for Handler() on every request. When that rebuilt the
// route tree each time — a new ServeMux, every route registered again, the
// whole console wired up — the cost was paid per request and paid under a mutex
// every other request had to queue behind. These are about the caching itself,
// which is the part a routing test would never notice.
//
// Identity is checked through the cache pointer rather than by comparing the
// handlers: the outermost middleware is an http.HandlerFunc, and func values
// are not comparable.

func setupServer() *Server {
	return NewSetup("", "127.0.0.1", 0)
}

func TestHandlerIsBuiltOnce(t *testing.T) {
	s := setupServer()

	s.Handler()
	first := s.handler.Load()
	if first == nil {
		t.Fatal("the handler was not cached at all")
	}
	for i := 0; i < 5; i++ {
		s.Handler()
		if got := s.handler.Load(); got != first {
			t.Fatalf("call %d rebuilt the handler; the per-request rebuild is back", i+2)
		}
	}
}

func TestHandlerIsBuiltOnceUnderConcurrentCalls(t *testing.T) {
	s := setupServer()

	const n = 32
	seen := make([]*http.Handler, n)
	var wg sync.WaitGroup
	for i := range seen {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Handler()
			seen[i] = s.handler.Load()
		}(i)
	}
	wg.Wait()

	if seen[0] == nil {
		t.Fatal("the handler was not cached at all")
	}
	for i := 1; i < n; i++ {
		if seen[i] != seen[0] {
			t.Fatalf("goroutine %d saw a different handler; a racing caller published its own", i)
		}
	}
}

// The install wizard turns a setup server into a running one. That switch is
// the only reason the tree was ever rebuilt, so it has to say so explicitly now
// — otherwise a freshly installed server keeps serving the setup form forever.
func TestSetupTransitionInvalidatesTheHandler(t *testing.T) {
	s := setupServer()

	before := s.Handler()
	beforePtr := s.handler.Load()
	if !servesSetupForm(before) {
		t.Fatal("a server awaiting setup did not serve the setup form")
	}

	s.mu.Lock()
	s.NeedsSetup = false
	s.Cfg = &config.ServerConfig{DataDir: t.TempDir(), Defaults: config.ServerDefaults{Space: "team"}}
	s.mu.Unlock()
	s.invalidateHandler()

	if s.handler.Load() != nil {
		t.Fatal("invalidateHandler left the old tree in place")
	}
	after := s.Handler()
	if s.handler.Load() == beforePtr {
		t.Fatal("the handler survived the setup transition; the server would still be serving the installer")
	}
	if servesSetupForm(after) {
		t.Error("the rebuilt handler still routes to the setup form")
	}
}

// servesSetupForm asks the tree whether GET /setup is wired, which is true for
// a server awaiting installation and false once it is running.
//
// GET rather than POST: the CSRF middleware refuses an unsafe method without a
// token before routing happens, so a POST answers 403 whether or not the route
// exists and would say "still in setup" forever.
func servesSetupForm(h http.Handler) bool {
	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code != http.StatusNotFound
}
