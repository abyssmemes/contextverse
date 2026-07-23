package acme

import (
	"path/filepath"
	"testing"
)

func TestValidate(t *testing.T) {
	if err := (Config{Enabled: true}).Validate(); err == nil {
		t.Fatal("expected email/domains error")
	}
	cfg := Config{Enabled: true, Email: "a@b.c", Domains: []string{"ex.com"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	want := filepath.Join(dir, "tls", "acme")
	if got := ResolveCacheDir(dir, ""); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
