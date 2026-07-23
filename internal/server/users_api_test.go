package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/server"
)

func TestUsersAndPolicyAPI(t *testing.T) {
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
	if err := store.AddUser("admin", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	adminTok, _, err := store.CreateToken("admin", "test")
	if err != nil {
		t.Fatal(err)
	}

	srv := server.New(cfg, store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	polBody, _ := json.Marshal(map[string]any{
		"description": "tenant scoped",
		"rules": []map[string]any{
			{"path": "spaces/", "capabilities": []string{"list"}},
			{"path": "spaces/acme", "capabilities": []string{"read"}},
			{"path": "spaces/acme/files", "capabilities": []string{"list"}},
			{"path": "spaces/acme/files/*", "capabilities": []string{"create", "read", "update", "delete", "list"}},
			{"path": "spaces/acme/head", "capabilities": []string{"read", "update"}},
			{"path": "spaces/acme/push", "capabilities": []string{"update"}},
			{"path": "sys/health", "capabilities": []string{"read"}},
		},
	})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/policies/tenant-acme", bytes.NewReader(polBody))
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put policy %d %s", res.StatusCode, body)
	}

	userBody, _ := json.Marshal(map[string]any{
		"name":        "tenant-acme",
		"role":        "contributor",
		"policies":    []string{"tenant-acme"},
		"issue_token": true,
	})
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/users", bytes.NewReader(userBody))
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create user %d %s", res.StatusCode, body)
	}
	var created map[string]any
	_ = json.Unmarshal(body, &created)
	tok, _ := created["token"].(string)
	if tok == "" {
		t.Fatal("expected issued token")
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("whoami %d %s", res.StatusCode, body)
	}
}
