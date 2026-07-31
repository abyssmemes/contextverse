package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/auth"
	"github.com/orkcom-tech/contextverse/internal/authz"
	"github.com/orkcom-tech/contextverse/internal/config"
)

// The worst kind of authorization bug is the one that looks like everything
// working. With no policy engine, allow() used to answer true for every read
// and every list, so a server whose policies failed to load served the whole
// data set to anyone holding a token — quietly, with a logged error nobody
// reads and a 200 on every request.
//
// Two locks: the server refuses to be built, and a Server that somehow has no
// engine refuses every request.

func TestNewRefusesAServerWhosePoliciesWillNotOpen(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Make the policy directory unreadable by replacing it with a file. Any
	// unreadable state does; this one needs no permission games that a test
	// running as root would defeat.
	policies := store.PoliciesDir()
	if err := os.RemoveAll(policies); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policies, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, err := New(&config.ServerConfig{DataDir: dir}, store)
	if err == nil {
		t.Fatal("built a server with no policy engine; it would have authorized everything")
	}
	if srv != nil {
		t.Error("returned a server alongside the error")
	}
}

func TestNewSucceedsWithReadablePolicies(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(&config.ServerConfig{DataDir: dir}, store)
	if err != nil {
		t.Fatalf("a well-formed data dir was refused: %v", err)
	}
	if srv.Authz == nil {
		t.Fatal("no engine on a server that reported success")
	}
	if err := srv.Spaces.Close(); err != nil {
		t.Error(err)
	}
}

// The second lock. A Server assembled without going through New — which is what
// the old fallback existed to paper over — must deny rather than wave through.
func TestAServerWithNoEngineAllowsNothing(t *testing.T) {
	s := &Server{Cfg: &config.ServerConfig{}}
	admin := &auth.Principal{User: "root", Role: auth.RoleAdmin, Policies: []string{"admin"}}

	cases := []struct {
		path string
		cap  authz.Capability
	}{
		{"spaces/team", authz.CapRead},
		{"spaces/team/files", authz.CapList},
		{"spaces/team/files/notes.md", authz.CapUpdate},
		{"sys/auth/users", authz.CapCreate},
	}
	for _, tc := range cases {
		if s.allow(admin, tc.path, tc.cap) {
			t.Errorf("%s on %s was allowed with no policy engine", tc.cap, tc.path)
		}
	}
	if s.canFileWrite(admin, "team", "notes.md") {
		t.Error("a file write was allowed with no policy engine")
	}
}

// And at the edge: the handler must answer 403, not 200, so the failure is
// visible to the caller rather than only in a log line.
func TestRequestsAreRefusedWithNoEngine(t *testing.T) {
	s := &Server{Cfg: &config.ServerConfig{}}
	admin := &auth.Principal{User: "root", Role: auth.RoleAdmin, Policies: []string{"admin"}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/spaces/team", nil)
	req = req.WithContext(context.WithValue(req.Context(), principalKey, admin))
	rec := httptest.NewRecorder()

	if s.requireCap(rec, req, "spaces/team", authz.CapRead) {
		t.Fatal("requireCap passed with no policy engine")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("answered %d, want 403", rec.Code)
	}
}

// The install wizard writes policies as part of setup, so a successful install
// must leave a readable engine behind — that path used to discard the error.
func TestSeededPoliciesOpen(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.PoliciesDir()); err != nil {
		t.Fatalf("OpenStore did not seed a policy directory: %v", err)
	}
	if _, err := authz.Open(store.PoliciesDir()); err != nil {
		t.Fatalf("the seeded policies do not open: %v", err)
	}
}
