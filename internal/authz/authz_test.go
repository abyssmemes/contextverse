package authz

import "testing"

func TestMatchPath(t *testing.T) {
	cases := []struct {
		pat, path string
		ok        bool
	}{
		{"*", "spaces/team/files/x", true},
		{"spaces/", "spaces/", true},
		{"spaces/team/files/*", "spaces/team/files/a/b", true},
		{"spaces/team/files/*", "spaces/other/files/a", false},
		{"spaces/team", "spaces/team", true},
		{"spaces/team", "spaces/team2", false},
		{"spaces/+/files", "spaces/team/files", true},
		{"spaces/+/files", "spaces/team/extra/files", false},
	}
	for _, c := range cases {
		_, ok := matchPath(c.pat, c.path)
		if ok != c.ok {
			t.Fatalf("matchPath(%q,%q)=%v want %v", c.pat, c.path, ok, c.ok)
		}
	}
}

func TestAllowDenyLongest(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Write(Policy{
		Name: "contrib",
		Rules: []Rule{
			{Path: "spaces/{{default}}/files/*", Capabilities: []Capability{CapRead, CapUpdate}},
			{Path: "spaces/{{default}}/files/identity/*", Capabilities: []Capability{CapRead}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	vars := Vars{"default": "team"}
	if !e.Allow([]string{"contrib"}, "spaces/team/files/team/x.md", CapUpdate, vars) {
		t.Fatal("expected update on team file")
	}
	if e.Allow([]string{"contrib"}, "spaces/team/files/identity/me.md", CapUpdate, vars) {
		t.Fatal("identity update should be denied (read-only most specific)")
	}
	if !e.Allow([]string{"contrib"}, "spaces/team/files/identity/me.md", CapRead, vars) {
		t.Fatal("identity read should allow")
	}
}

func TestDenyWins(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Write(Policy{Name: "a", Rules: []Rule{
		{Path: "spaces/team/files/*", Capabilities: []Capability{CapRead, CapUpdate}},
	}})
	_ = e.Write(Policy{Name: "b", Rules: []Rule{
		{Path: "spaces/team/files/secret.md", Capabilities: []Capability{CapDeny}},
	}})
	vars := Vars{"default": "team"}
	if e.Allow([]string{"a", "b"}, "spaces/team/files/secret.md", CapRead, vars) {
		t.Fatal("deny should win")
	}
}

func TestAdminStar(t *testing.T) {
	dir := t.TempDir()
	if err := SeedBuiltins(dir, "team"); err != nil {
		t.Fatal(err)
	}
	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	vars := Vars{"default": "team"}
	if !e.Allow([]string{"admin"}, "sys/backends", CapUpdate, vars) {
		t.Fatal("admin should update backends")
	}
	if !e.Allow([]string{"admin"}, "sys/backends", CapSudo, vars) {
		t.Fatal("admin should have sudo")
	}
	if e.Allow([]string{"viewer"}, "spaces/team/push", CapUpdate, vars) {
		t.Fatal("viewer must not push")
	}
	if !e.Allow([]string{"contributor"}, "spaces/team/push", CapUpdate, vars) {
		t.Fatal("contributor should push")
	}
}

func TestPerUserDenyWins(t *testing.T) {
	dir := t.TempDir()
	if err := SeedBuiltins(dir, "team"); err != nil {
		t.Fatal(err)
	}
	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	vars := Vars{"default": "team"}
	path := "spaces/team/files/team/principles.md"
	if !e.AllowUser("bob", []string{"contributor"}, path, CapUpdate, vars) {
		t.Fatal("contributor bob should update before deny")
	}
	if err := e.AddUserRule("bob", Rule{Path: path, Capabilities: []Capability{CapDeny}}); err != nil {
		t.Fatal(err)
	}
	if e.AllowUser("bob", []string{"contributor"}, path, CapUpdate, vars) {
		t.Fatal("per-user deny must win")
	}
	if e.AllowUser("bob", []string{"contributor"}, path, CapRead, vars) {
		t.Fatal("CapDeny blocks all caps on path")
	}
	// other users unaffected
	if !e.AllowUser("alice", []string{"contributor"}, path, CapUpdate, vars) {
		t.Fatal("alice should still update")
	}
}

func TestPerUserAllowExtra(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Write(Policy{Name: "viewer", Rules: []Rule{
		{Path: "spaces/team/files/*", Capabilities: []Capability{CapRead, CapList}},
	}})
	vars := Vars{"default": "team"}
	secret := "spaces/team/files/secret/x.md"
	if e.AllowUser("alice", []string{"viewer"}, secret, CapUpdate, vars) {
		t.Fatal("viewer must not update")
	}
	if err := e.AddUserRule("alice", Rule{
		Path:         "spaces/team/files/secret/*",
		Capabilities: []Capability{CapRead, CapUpdate},
	}); err != nil {
		t.Fatal(err)
	}
	if !e.AllowUser("alice", []string{"viewer"}, secret, CapUpdate, vars) {
		t.Fatal("per-user allow should grant update")
	}
}
