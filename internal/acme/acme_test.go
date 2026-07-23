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
	if cfg.NormalizedChallenge() != ChallengeHTTP01 {
		t.Fatal(cfg.NormalizedChallenge())
	}
	dir := t.TempDir()
	want := filepath.Join(dir, "tls", "acme")
	if got := ResolveCacheDir(dir, ""); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestValidateDNS01(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Email:     "a@b.c",
		Domains:   []string{"ex.com"},
		Challenge: ChallengeDNS01,
		DNS:       DNSConfig{Provider: ProviderCloudflare},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := cfg
	bad.DNS.Provider = "route53"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected unsupported provider")
	}
	badCh := cfg
	badCh.Challenge = "tls-alpn-01"
	if err := badCh.Validate(); err == nil {
		t.Fatal("expected bad challenge")
	}
}
