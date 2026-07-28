package localui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/storage"
	"github.com/orkcom-tech/contextverse/internal/testspace"
)

// The console shares its templates with the server's, which is what keeps the
// two from drifting into different products — and also what let a link to a
// server-only page ship into the local console, where it 404s.
//
// The old nav test asserted that every page was *present* in the menu. Presence
// is not validity: it never asked whether a link on a local page resolves to a
// route this server registers.

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := testspace.Legacy(t)
	b, err := storage.Open(storage.OpenOptions{SpaceRoot: root, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{SpaceRoot: root, FileLog: &storage.FileLog{Backend: b}, Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ln.Close() })
	return s, root
}

// get fetches a page through the real handler, session and all.
func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.session})
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	return rec
}

var hrefRe = regexp.MustCompile(`href="(/[^"]*)"`)

// Every internal link the console renders has to lead somewhere it serves.
func TestEveryLinkOnEveryPageResolves(t *testing.T) {
	s, _ := newTestServer(t)

	pages := []string{"/ui/spaces/local", "/ui/graph"}
	for _, page := range pages {
		rec := get(t, s, page)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d", page, rec.Code)
		}
		for _, m := range hrefRe.FindAllStringSubmatch(rec.Body.String(), -1) {
			href := m[1]
			// Static assets and the auth exchange are served, not rendered.
			if strings.HasPrefix(href, "/ui/static/") || strings.HasPrefix(href, "/auth") {
				continue
			}
			// A fragment addresses the current page, not another route.
			if i := strings.IndexByte(href, '#'); i >= 0 {
				href = href[:i]
				if href == "" {
					continue
				}
			}
			target := get(t, s, href)
			if target.Code == http.StatusNotFound {
				t.Errorf("%s links to %s, which this console does not serve", page, href)
			}
		}
	}
}

// The specific one that shipped: "All spaces" belongs to a server with more
// than one, and the local console has exactly one.
func TestNoServerOnlyNavigationLeaksIn(t *testing.T) {
	s, _ := newTestServer(t)

	serverOnly := []string{"/ui/spaces\"", "/ui/users", "/ui/policies", "/ui/audit", "/ui/webhooks", "/ui/backends", "/ui/logout"}
	for _, page := range []string{"/ui/spaces/local", "/ui/graph"} {
		body := get(t, s, page).Body.String()
		for _, bad := range serverOnly {
			if strings.Contains(body, `href="`+bad) {
				t.Errorf("%s offers %s, which only exists on a server", page, strings.TrimSuffix(bad, `"`))
			}
		}
	}
}

// The console renders the same space as every other surface, so an upgraded
// space must not look empty here either.
func TestConsoleSeesAnUpgradedSpace(t *testing.T) {
	s, _ := newTestServer(t)

	body := get(t, s, "/ui/spaces/local").Body.String()
	for _, want := range []string{"identity/me.md", "team/principles.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("%s is in the space but missing from the console:\n", want)
		}
	}
}

// Long content must scroll inside its own box. The Mermaid block on the graph
// page was pushing the page sideways, which is how a console starts feeling
// broken before anything actually is.
func TestWideContentIsContained(t *testing.T) {
	s, _ := newTestServer(t)
	body := get(t, s, "/ui/graph").Body.String()

	if !strings.Contains(body, "file-pre") {
		t.Fatal("the diagram source block is missing")
	}
	css := get(t, s, "/ui/static/app.css").Body.String()
	for _, rule := range []string{".app-main { overflow-x: hidden; }", "overflow-x: auto"} {
		if !strings.Contains(css, rule) {
			t.Errorf("stylesheet lacks %q, so wide content can push the page sideways", rule)
		}
	}
}
