package acme

import "testing"

func TestValidate(t *testing.T) {
	if err := (Config{Enabled: true}).Validate(); err == nil {
		t.Fatal("expected email/domains error")
	}
	cfg := Config{Enabled: true, Email: "a@b.c", Domains: []string{"ex.com"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if ResolveCacheDir("/data", "") != "/data/tls/acme" {
		t.Fatal(ResolveCacheDir("/data", ""))
	}
}
