package auth

import "testing"

func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()
	if _, ok := r.Get("token"); !ok {
		t.Fatal("token missing")
	}
	if _, ok := r.Get("userpass"); !ok {
		t.Fatal("userpass missing")
	}
	if _, ok := r.Get("oidc"); ok {
		t.Fatal("oidc must not be registered in OSS")
	}
	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("names=%v", names)
	}
}
