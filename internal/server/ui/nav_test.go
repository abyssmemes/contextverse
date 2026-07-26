package ui

import (
	"bytes"
	"strings"
	"testing"
)

// navPage builds the real Page the handlers pass, rather than a stand-in — a
// test struct of its own would keep passing while the template drifted away
// from the type the server actually renders with.
func navPage(active string) Page {
	return Page{Title: "Test", User: "admin", Role: "admin", Active: active, Version: "test"}
}

func renderTemplate(t *testing.T, name string, data any) string {
	t.Helper()
	tp, err := templates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	var buf bytes.Buffer
	if err := tp.ExecuteTemplate(&buf, name, data); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return buf.String()
}

// The sidebar is grouped with the same headings the CLI uses. Losing a group,
// or losing a page out of the nav, makes a shipped feature unreachable in the
// browser while still existing everywhere else.
func TestSidebarIsGroupedLikeTheCLI(t *testing.T) {
	out := renderTemplate(t, "app-open", navPage("dash"))

	for _, group := range []string{"Context space", "Sync and storage", "Administer"} {
		if !strings.Contains(out, group) {
			t.Errorf("sidebar is missing the %q group:\n%s", group, out)
		}
	}
}

func TestEveryPageIsReachableFromBothNavs(t *testing.T) {
	pages := []string{
		"/ui/spaces",
		"/ui/freshness",
		"/ui/backends",
		"/ui/users",
		"/ui/policies",
		"/ui/audit",
		"/ui/webhooks",
	}
	for _, name := range []string{"app-open", "topbar"} {
		out := renderTemplate(t, name, navPage("dash"))
		for _, href := range pages {
			if !strings.Contains(out, `href="`+href+`"`) {
				t.Errorf("%s does not link to %s", name, href)
			}
		}
	}
}

// Active marking is what tells the operator where they are; a page whose Active
// key does not match its nav entry renders a menu with nothing highlighted.
func TestActivePageIsMarked(t *testing.T) {
	cases := map[string]string{
		"spaces":    "/ui/spaces",
		"users":     "/ui/users",
		"policies":  "/ui/policies",
		"backends":  "/ui/backends",
		"audit":     "/ui/audit",
		"webhooks":  "/ui/webhooks",
		"freshness": "/ui/freshness",
	}
	for active, href := range cases {
		out := renderTemplate(t, "app-open", navPage(active))
		marker := `href="` + href + `" class="active"`
		if !strings.Contains(out, marker) {
			t.Errorf("Active=%q did not mark %s as current", active, href)
		}
		if n := strings.Count(out, `class="active"`); n != 1 {
			t.Errorf("Active=%q marked %d entries, want exactly 1", active, n)
		}
	}
}

// The nav is only rendered for a signed-in operator; an anonymous shell that
// still listed admin pages would advertise routes the visitor cannot open.
func TestSignedOutShellHasNoAdminNav(t *testing.T) {
	out := renderTemplate(t, "topbar", Page{Title: "Login", Active: "login", Version: "test"})
	if strings.Contains(out, "/ui/users") {
		t.Errorf("signed-out topbar exposes admin navigation:\n%s", out)
	}
}
