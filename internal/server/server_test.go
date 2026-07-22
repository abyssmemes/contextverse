package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abyssmemes/contextverse/internal/audit"
	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/server"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
	"github.com/abyssmemes/contextverse/internal/webhooks"
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

	// audit recorded push + deny
	entries, err := srv.Audit.Query(audit.Filter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var sawPush, sawDeny bool
	for _, e := range entries {
		if e.Action == "space.push" && e.Result == audit.ResultSuccess {
			sawPush = true
		}
		if e.Action == "authz.deny" {
			sawDeny = true
		}
	}
	if !sawPush {
		t.Fatal("expected space.push audit entry")
	}
	if !sawDeny {
		t.Fatal("expected authz.deny audit entry")
	}

	// secret-scan blocks known patterns
	head2 := headBody.Space
	// refresh head after push
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/spaces/team/head", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(res.Body).Decode(&headBody)
	res.Body.Close()
	head2 = headBody.Space
	leak, _ := json.Marshal(map[string]any{
		"expected_head": head2,
		"ops": []map[string]string{{
			"op":          "put",
			"path":        "leak.md",
			"content_b64": "QUtJQUlPU0ZPRE5ON0VYQU1QTEUK", // AKIAIOSFODNN7EXAMPLE\n base64
		}},
	})
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/spaces/team/push", bytes.NewReader(leak))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("secret scan want 422 got %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/audit?limit=20", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("audit list %d %s", res.StatusCode, b)
	}
}

func TestQuotaAndRateLimit(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.ServerConfig{
		Mode:    config.ModeServer,
		DataDir: dir,
		Listen:  config.ListenConfig{Address: "127.0.0.1", Port: 0},
		Backend: config.Backend{Driver: "local"},
		Quotas:  config.QuotasConfig{MaxFileSize: 32, MaxSpaceSize: 1 << 20, MaxFiles: 5000},
		RateLimit: config.RateLimitConfig{Enabled: true, RequestsPerMinute: 3, AuthPerMinute: 10},
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

	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/auth/whoami", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("whoami %d on %d", res.StatusCode, i)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate limit want 429 got %d %s", res.StatusCode, b)
	}
	if res.Header.Get("Retry-After") == "" {
		t.Fatal("expected Retry-After")
	}

	tok2, _, err := store.CreateToken("admin", "quota")
	if err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("x"), 64)
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/api/v1/spaces/team/files/too-big.md", bytes.NewReader(big))
	req.Header.Set("Authorization", "Bearer "+tok2)
	req.Header.Set("If-Match", `""`)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("quota want 413 got %d %s", res.StatusCode, body)
	}
}

func TestMetricsAndSSE(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.ServerConfig{
		Mode:    config.ModeServer,
		DataDir: dir,
		Listen:  config.ListenConfig{Address: "127.0.0.1", Port: 0},
		Backend: config.Backend{Driver: "local"},
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

	res, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Contains(body, []byte("contextd_up 1")) {
		t.Fatalf("metrics %d %s", res.StatusCode, body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events?scopes=team", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("sse %d %s", res.StatusCode, b)
	}

	// publish via webhook path
	srv.Dispatch.Emit(webhooks.Event{Type: "space.push", Space: "team", Scope: "team/x.md", Actor: "admin"})

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var got string
		for {
			n, readErr := res.Body.Read(buf)
			if n > 0 {
				got += string(buf[:n])
				if strings.Contains(got, "space.push") {
					done <- got
					return
				}
			}
			if readErr != nil {
				done <- got
				return
			}
		}
	}()
	select {
	case got := <-done:
		if !strings.Contains(got, "space.push") {
			t.Fatalf("expected sse event, got %q", got)
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timeout waiting for sse event")
	}
	cancel()
}
