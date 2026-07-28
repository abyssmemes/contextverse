package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/auth"
	"github.com/orkcom-tech/contextverse/internal/config"
	"github.com/orkcom-tech/contextverse/internal/server"
	"github.com/orkcom-tech/contextverse/internal/spacesvc"
)

// adminFixture is a server with an admin who can manage users, and a scout who
// holds tokens worth revoking.
func adminFixture(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.ServerConfig{
		Mode:     config.ModeServer,
		DataDir:  dir,
		Listen:   config.ListenConfig{Address: "127.0.0.1", Port: 0},
		Backend:  config.Backend{Driver: "local"},
		Defaults: config.ServerDefaults{Space: "team"},
	}
	if err := config.SaveServer(cfg); err != nil {
		t.Fatal(err)
	}
	store, err := auth.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, role := range map[string]auth.Role{"admin": auth.RoleAdmin, "scout": auth.RoleAdmin} {
		if err := store.AddUser(name, role); err != nil {
			t.Fatal(err)
		}
	}
	adminTok, _, err := store.CreateToken("admin", "test")
	if err != nil {
		t.Fatal(err)
	}
	svc := &spacesvc.Service{DataDir: dir}
	if _, err := svc.Create(t.Context(), "team", "solo-default", true); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.New(cfg, store).Handler())
	t.Cleanup(ts.Close)
	return ts, adminTok
}

// mintToken issues a fresh token for a user through the API.
func mintToken(t *testing.T, ts *httptest.Server, adminToken, user string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/users/"+user+"/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("mint token returned %d", res.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" {
		t.Fatal("mint returned an empty token")
	}
	return out.Token
}

// getSpaces is the cheapest authenticated call, used here only to ask whether a
// token is still accepted.
func getSpaces(t *testing.T, ts *httptest.Server, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/spaces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	return res.StatusCode
}

// The API could mint tokens and never take them back. Revocation existed as
// `contextd user disable` on the machine and as a button in the web console;
// an operator working over the API — or anything managing a fleet — had no way
// to cut off a credential they believed was leaked.
func TestRevokingAUsersTokensStopsThem(t *testing.T) {
	ts, adminToken := adminFixture(t)

	// Give the user a token and prove it works.
	minted := mintToken(t, ts, adminToken, "scout")
	if code := getSpaces(t, ts, minted); code != http.StatusOK {
		t.Fatalf("a freshly minted token was refused with %d", code)
	}

	// Revoke, and count.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/users/scout/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("revoke returned %d", res.StatusCode)
	}
	var out struct {
		Revoked int `json:"revoked"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	// "revoked 0" and "revoked 3" are different answers when you are closing a
	// door you think was open.
	if out.Revoked < 1 {
		t.Errorf("revoked %d tokens; the user had at least one", out.Revoked)
	}

	if code := getSpaces(t, ts, minted); code == http.StatusOK {
		t.Error("the revoked token still works")
	}
}

// Disabling has to drop live tokens too, or it closes the login and leaves the
// access — which is the shape of the cloud bug this exists to fix.
func TestDisablingAUserDropsTheirLiveTokens(t *testing.T) {
	ts, adminToken := adminFixture(t)

	minted := mintToken(t, ts, adminToken, "scout")
	if code := getSpaces(t, ts, minted); code != http.StatusOK {
		t.Fatalf("a freshly minted token was refused with %d", code)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/users/scout/disable", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("disable returned %d", res.StatusCode)
	}

	if code := getSpaces(t, ts, minted); code == http.StatusOK {
		t.Error("a disabled user's token still works")
	}
}
