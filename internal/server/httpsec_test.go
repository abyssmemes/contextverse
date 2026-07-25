package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/server"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
)

// consoleFixture spins up a server with a real admin session so the console
// routes can be exercised end to end.
type consoleFixture struct {
	ts     *httptest.Server
	client *http.Client
	token  string
}

func newConsole(t *testing.T) *consoleFixture {
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
	if err := store.AddUser("admin", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	token, _, err := store.CreateToken("admin", "console")
	if err != nil {
		t.Fatal(err)
	}
	svc := &spacesvc.Service{DataDir: dir}
	if _, err := svc.Create(context.Background(), "team", "solo-default", true); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(server.New(cfg, store).Handler())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// Log in the way a browser does, so the session cookie lands in the jar.
	res, err := c.PostForm(ts.URL+"/ui/login", url.Values{"token": {token}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	f := &consoleFixture{ts: ts, client: c, token: token}
	// The very first login POST has no CSRF cookie yet, so it is refused by
	// design; do the GET-then-POST dance the real UI does.
	if res.StatusCode == http.StatusForbidden {
		if _, err := c.Get(ts.URL + "/ui/login"); err != nil {
			t.Fatal(err)
		}
		res, err = c.PostForm(ts.URL+"/ui/login", url.Values{
			"token":      {token},
			"csrf_token": {f.csrf(t)},
		})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	if res.StatusCode >= 400 {
		t.Fatalf("console login failed: %d", res.StatusCode)
	}
	return f
}

func (f *consoleFixture) csrf(t *testing.T) string {
	t.Helper()
	u, _ := url.Parse(f.ts.URL)
	for _, c := range f.client.Jar.Cookies(u) {
		if c.Name == "cv_csrf" {
			return c.Value
		}
	}
	t.Fatal("no CSRF cookie was issued")
	return ""
}

func TestConsoleIssuesACSRFCookieOnFirstView(t *testing.T) {
	f := newConsole(t)
	res, err := f.client.Get(f.ts.URL + "/ui/spaces")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("spaces page: %d", res.StatusCode)
	}
	if f.csrf(t) == "" {
		t.Fatal("expected a CSRF token")
	}
}

func TestConsoleWriteWithoutTokenIsRefused(t *testing.T) {
	f := newConsole(t)
	if _, err := f.client.Get(f.ts.URL + "/ui/spaces"); err != nil {
		t.Fatal(err)
	}
	res, err := f.client.PostForm(f.ts.URL+"/ui/spaces", url.Values{"name": {"forged"}})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a session cookie alone must not create a space, got %d", res.StatusCode)
	}
}

func TestConsoleWriteWithWrongTokenIsRefused(t *testing.T) {
	f := newConsole(t)
	if _, err := f.client.Get(f.ts.URL + "/ui/spaces"); err != nil {
		t.Fatal(err)
	}
	res, err := f.client.PostForm(f.ts.URL+"/ui/spaces", url.Values{
		"name":       {"forged"},
		"csrf_token": {"not-the-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a guessed token must not pass, got %d", res.StatusCode)
	}
}

func TestConsoleWriteWithTokenSucceeds(t *testing.T) {
	f := newConsole(t)
	if _, err := f.client.Get(f.ts.URL + "/ui/spaces"); err != nil {
		t.Fatal(err)
	}
	res, err := f.client.PostForm(f.ts.URL+"/ui/spaces", url.Values{
		"name":       {"legit"},
		"template":   {"solo-default"},
		"csrf_token": {f.csrf(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		t.Fatalf("a form carrying the token must be accepted, got %d", res.StatusCode)
	}
}

func TestRenderedFormsCarryTheToken(t *testing.T) {
	f := newConsole(t)
	res, err := f.client.Get(f.ts.URL + "/ui/spaces")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body := readBody(t, res)
	want := `name="csrf_token" value="` + f.csrf(t) + `"`
	if !strings.Contains(body, want) {
		t.Fatalf("rendered page does not embed the CSRF token")
	}
}

func TestConsolePageCarriesANonceMatchingItsPolicy(t *testing.T) {
	f := newConsole(t)
	res, err := f.client.Get(f.ts.URL + "/ui/spaces")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	csp := res.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("console page has no content security policy")
	}
	if strings.Contains(csp, "unsafe-inline") && strings.Contains(csp, "script-src") {
		// unsafe-inline is fine for styles but must not appear in script-src.
		for _, part := range strings.Split(csp, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "script-src") && strings.Contains(part, "unsafe-inline") {
				t.Fatalf("script-src still allows inline: %q", part)
			}
		}
	}
	body := readBody(t, res)
	nonce := nonceFromCSP(csp)
	if nonce == "" {
		t.Fatalf("policy has no nonce: %q", csp)
	}
	if !strings.Contains(body, `nonce="`+nonce+`"`) {
		t.Fatal("inline script does not carry the nonce from the policy")
	}
}

func TestAPIGetsALockedDownPolicy(t *testing.T) {
	f := newConsole(t)
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/api/v1/spaces", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("API responses should not be allowed to load anything, got %q", csp)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff on the API")
	}
	if res.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("missing frame denial")
	}
}

func TestAPIWritesNeedNoCSRFToken(t *testing.T) {
	f := newConsole(t)
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/api/v1/spaces",
		strings.NewReader(`{"name":"viaapi","template":"solo-default"}`))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusForbidden {
		t.Fatal("bearer-authenticated API calls must not be subject to CSRF")
	}
}

func TestOversizedJSONBodyIsRefused(t *testing.T) {
	f := newConsole(t)
	huge := strings.Repeat("a", 9<<20)
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/api/v1/spaces",
		strings.NewReader(`{"name":"`+huge+`"}`))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.client.Do(req)
	if err != nil {
		// A refused body may surface as a broken pipe on the client side, which
		// is still the cap doing its job.
		return
	}
	defer res.Body.Close()
	if res.StatusCode < 400 {
		t.Fatalf("a 9 MiB JSON body should not be accepted, got %d", res.StatusCode)
	}
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func nonceFromCSP(csp string) string {
	const marker = "'nonce-"
	i := strings.Index(csp, marker)
	if i < 0 {
		return ""
	}
	rest := csp[i+len(marker):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return ""
	}
	return rest[:j]
}
