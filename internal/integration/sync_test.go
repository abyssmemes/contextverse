//go:build integration

// Package integration holds Docker-backed end-to-end tests.
// Run: make test-integration  (starts MinIO + Postgres via compose).
package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/server"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
	"github.com/abyssmemes/contextverse/internal/syncclient"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type harness struct {
	t     *testing.T
	dir   string
	cfg   *config.ServerConfig
	store *auth.Store
	token string
	ts    *httptest.Server
	space string
}

func startServer(t *testing.T, backend config.Backend, space string) *harness {
	t.Helper()
	dir := t.TempDir()
	if space == "" {
		space = "team"
	}
	// Isolate shared backends across parallel tests.
	if backend.Driver == "s3" && backend.S3Prefix == "" {
		backend.S3Prefix = "it/" + strings.ReplaceAll(t.Name(), "/", "_")
	}
	cfg := &config.ServerConfig{
		Mode:     config.ModeServer,
		DataDir:  dir,
		Listen:   config.ListenConfig{Address: "127.0.0.1", Port: 0},
		Backend:  backend,
		Defaults: config.ServerDefaults{Space: space},
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
	_ = store.SetPassword("admin", "admin-pass")
	token, _, err := store.CreateToken("admin", "it")
	if err != nil {
		t.Fatal(err)
	}
	svc := &spacesvc.Service{DataDir: dir, Backend: backend}
	if _, err := svc.Create(context.Background(), space, "solo-default", true); err != nil {
		t.Fatalf("create space: %v", err)
	}
	srv := server.New(cfg, store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, dir: dir, cfg: cfg, store: store, token: token, ts: ts, space: space}
}

func (h *harness) do(method, path string, body any) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.ts.URL+path, rdr)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func (h *harness) head() string {
	h.t.Helper()
	res := h.do(http.MethodGet, "/api/v1/spaces/"+h.space+"/head", nil)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("head %d %s", res.StatusCode, b)
	}
	var body struct {
		Space string `json:"space"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	return body.Space
}

func (h *harness) push(path, content, expectedHead string) {
	h.t.Helper()
	body := map[string]any{
		"expected_head": expectedHead,
		"ops": []map[string]string{
			{
				"op":          "put",
				"path":        path,
				"content_b64": base64.StdEncoding.EncodeToString([]byte(content)),
			},
		},
	}
	res := h.do(http.MethodPost, "/api/v1/spaces/"+h.space+"/push", body)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("push %d %s", res.StatusCode, b)
	}
}

func (h *harness) getFile(path string) string {
	h.t.Helper()
	res := h.do(http.MethodGet, "/api/v1/spaces/"+h.space+"/files/"+path, nil)
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		h.t.Fatalf("get %d %s", res.StatusCode, b)
	}
	return string(b)
}

func s3Backend() config.Backend {
	return config.Backend{
		Driver:      "s3",
		S3Endpoint:  envOr("CONTEXTVERSE_S3_ENDPOINT", "http://127.0.0.1:9000"),
		S3Region:    "us-east-1",
		S3Bucket:    envOr("CONTEXTVERSE_S3_BUCKET", "contextverse"),
		S3AccessKey: envOr("CONTEXTVERSE_S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey: envOr("CONTEXTVERSE_S3_SECRET_KEY", "minioadmin"),
		S3PathStyle: true,
	}
}

func sqlBackend() config.Backend {
	return config.Backend{
		Driver: "sql",
		SQLDSN: envOr("CONTEXTVERSE_SQL_DSN", "postgres://contextverse:contextverse@127.0.0.1:5432/contextverse?sslmode=disable"),
	}
}

func runPushPullVariant(t *testing.T, backend config.Backend) {
	t.Helper()
	h := startServer(t, backend, "itspace")
	res := h.do(http.MethodGet, "/health", nil)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("health %d", res.StatusCode)
	}
	head := h.head()
	path := "docs/hello.md"
	content := "# Hello\n\nfrom " + backend.Driver
	h.push(path, content, head)
	got := h.getFile(path)
	if got != content {
		t.Fatalf("file content: got %q want %q", got, content)
	}
	// second space must not collide on shared S3/SQL
	h2 := startServer(t, backend, "itspace-b")
	head2 := h2.head()
	h2.push(path, "other-space", head2)
	if h.getFile(path) != content {
		t.Fatal("space A corrupted by space B write")
	}
	if h2.getFile(path) != "other-space" {
		t.Fatal("space B content wrong")
	}
}

func TestPushPull_Local(t *testing.T) {
	runPushPullVariant(t, config.Backend{Driver: "local"})
}

func TestPushPull_S3(t *testing.T) {
	// Probe MinIO before spinning a full server.
	b := s3Backend()
	b.S3Prefix = "probe/" + t.Name()
	svc := &spacesvc.Service{DataDir: t.TempDir(), Backend: b}
	if _, err := svc.OpenBackend("probe"); err != nil {
		t.Skipf("s3 unavailable: %v", err)
	}
	runPushPullVariant(t, s3Backend())
}

func TestPushPull_SQL(t *testing.T) {
	b := sqlBackend()
	svc := &spacesvc.Service{DataDir: t.TempDir(), Backend: b}
	if _, err := svc.OpenBackend("probe"); err != nil {
		t.Skipf("sql unavailable: %v", err)
	}
	runPushPullVariant(t, b)
}

// consoleLogin signs into the web console the way a browser does and returns a
// client carrying the session.
//
// The console is CSRF-protected by double submit: a POST is refused unless it
// echoes back the token from the `cv_csrf` cookie, and that cookie is only
// minted on a GET. Posting the credentials straight away — which this test used
// to do — is exactly the cross-site shape the protection exists to reject, so it
// earns a 403 rather than a session.
func consoleLogin(t *testing.T, h *harness) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// GET first: this is what mints the CSRF cookie.
	res, err := client.Get(h.ts.URL + "/ui/login")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	base, _ := url.Parse(h.ts.URL)
	csrf := ""
	for _, c := range jar.Cookies(base) {
		if c.Name == "cv_csrf" {
			csrf = c.Value
			break
		}
	}
	if csrf == "" {
		t.Fatal("console did not set a cv_csrf cookie on GET /ui/login")
	}

	res, err = client.PostForm(h.ts.URL+"/ui/login", url.Values{
		"token":      {h.token},
		"csrf_token": {csrf},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode >= 400 {
		t.Fatalf("console login failed: status=%d", res.StatusCode)
	}
	for _, c := range jar.Cookies(base) {
		if c.Name == "cv_session" {
			return client
		}
	}
	t.Fatalf("no session cookie after login; status=%d", res.StatusCode)
	return nil
}

func TestMarkdownPreviewUI(t *testing.T) {
	h := startServer(t, config.Backend{Driver: "local"}, "team")
	head := h.head()
	h.push("readme.md", "# Title\n\n**bold**", head)

	client := consoleLogin(t, h)

	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+"/ui/spaces/team/files/readme.md?view=preview", nil)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("preview %d %s", res.StatusCode, body)
	}
	html := string(body)
	if !strings.Contains(html, "<h1") || !strings.Contains(html, "Title") {
		t.Fatalf("expected rendered markdown HTML, got:\n%s", html)
	}
	if !strings.Contains(html, "<strong>") && !strings.Contains(html, "<b>") {
		// GFM may render **bold** as <strong>
		t.Fatalf("expected bold markup in preview:\n%s", html)
	}
}

func TestDaemonPollPull(t *testing.T) {
	h := startServer(t, config.Backend{Driver: "local"}, "team")
	clientRoot := t.TempDir()
	cfg := &config.Config{
		Mode:      config.ModeClient,
		SpaceRoot: clientRoot,
		Server: config.ClientServer{
			URL:   h.ts.URL,
			Space: "team",
		},
		Daemon: config.DaemonConfig{IntervalSec: 1},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := syncclient.WriteToken(clientRoot, h.token); err != nil {
		t.Fatal(err)
	}
	// Seed local last head from server so first poll sees "unchanged"
	head := h.head()
	cfg.Sync.LastHead = head
	_ = config.Save(cfg)

	pulled, err := syncclient.PollOnce(context.Background(), clientRoot, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if pulled {
		t.Fatal("expected no pull when head unchanged")
	}

	h.push("docs/from-server.md", "daemon-payload", head)
	pulled, err = syncclient.PollOnce(context.Background(), clientRoot, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !pulled {
		t.Fatal("expected pull after head change")
	}
	raw, err := os.ReadFile(filepath.Join(clientRoot, "docs/from-server.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "daemon-payload" {
		t.Fatalf("got %q", raw)
	}
}
