package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/server"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
)

// scopedFixture builds a server with two spaces where "scout" may only read alpha.
func scopedFixture(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.ServerConfig{
		Mode:     config.ModeServer,
		DataDir:  dir,
		Listen:   config.ListenConfig{Address: "127.0.0.1", Port: 0},
		Backend:  config.Backend{Driver: "local"},
		Defaults: config.ServerDefaults{Space: "alpha"},
	}
	if err := config.SaveServer(cfg); err != nil {
		t.Fatal(err)
	}
	store, err := auth.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddUser("scout", auth.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPolicies("scout", []string{"alpha-only"}); err != nil {
		t.Fatal(err)
	}
	eng, err := authz.Open(store.PoliciesDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Write(authz.Policy{
		Name: "alpha-only",
		Rules: []authz.Rule{
			{Path: "spaces/alpha", Capabilities: []authz.Capability{authz.CapRead}},
			{Path: "spaces/alpha/files/*", Capabilities: []authz.Capability{authz.CapRead, authz.CapList}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	token, _, err := store.CreateToken("scout", "test")
	if err != nil {
		t.Fatal(err)
	}
	svc := &spacesvc.Service{DataDir: dir}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := svc.Create(context.Background(), name, "solo-default", true); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(server.New(cfg, store).Handler())
	t.Cleanup(ts.Close)
	return ts, token
}

func TestSpaceListingIsFilteredByACL(t *testing.T) {
	ts, token := scopedFixture(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/spaces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Spaces []struct {
			Name string `json:"name"`
		} `json:"spaces"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(body.Spaces) != 1 || body.Spaces[0].Name != "alpha" {
		t.Fatalf("listing must be filtered to alpha, got %+v", body.Spaces)
	}
}

func TestEventScopesAreIntersectedWithGrants(t *testing.T) {
	ts, token := scopedFixture(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/events?scopes=beta", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("subscribing to a foreign space must be 403, got %d %s", res.StatusCode, b)
	}
}

func TestUISpacesHidesForeignSpaces(t *testing.T) {
	ts, token := scopedFixture(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/ui/spaces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ui spaces %d %s", res.StatusCode, page)
	}
	if !strings.Contains(string(page), "alpha") {
		t.Fatal("granted space must be listed")
	}
	if strings.Contains(string(page), ">beta<") || strings.Contains(string(page), "/ui/spaces/beta") {
		t.Fatalf("foreign space leaked into the console:\n%s", page)
	}
}

func TestUIBackendsRequiresReadCap(t *testing.T) {
	ts, token := scopedFixture(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/ui/backends", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("backend config must not be readable by a scoped reader, got %d", res.StatusCode)
	}
}
