package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/server"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
)

func TestServerPushPullFlow(t *testing.T) {
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
	token, _, err := store.CreateToken("admin", "test")
	if err != nil {
		t.Fatal(err)
	}
	svc := &spacesvc.Service{DataDir: dir}
	if _, err := svc.Create(context.Background(), "team", "solo-default", true); err != nil {
		t.Fatal(err)
	}

	srv := server.New(cfg, store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("health: %v %v", err, res)
	}
	res.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("whoami %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/spaces/team/head", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var headBody struct {
		Space string `json:"space"`
	}
	_ = json.NewDecoder(res.Body).Decode(&headBody)
	res.Body.Close()
	if headBody.Space == "" {
		t.Fatal("expected non-empty head after seed")
	}

	pushBody, _ := json.Marshal(map[string]any{
		"expected_head": headBody.Space,
		"ops": []map[string]string{
			{
				"op":          "put",
				"path":        "team/principles.md",
				"content_b64": "bmV3LXByaW5jaXBsZXM=", // new-principles
			},
		},
	})
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/spaces/team/push", bytes.NewReader(pushBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("push %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/spaces/team/files/team/principles.md", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(data) != "new-principles" {
		t.Fatalf("got %q", data)
	}

	// viewer cannot push
	if err := store.AddUser("bob", auth.RoleViewer); err != nil {
		t.Fatal(err)
	}
	vtok, _, err := store.CreateToken("bob", "v")
	if err != nil {
		t.Fatal(err)
	}
	_ = time.Now()
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/spaces/team/push", bytes.NewReader(pushBody))
	req.Header.Set("Authorization", "Bearer "+vtok)
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer push want 403 got %d", res.StatusCode)
	}
}
