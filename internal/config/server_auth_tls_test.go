package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectCloudOnlyAuth(t *testing.T) {
	dir := t.TempDir()
	path := ServerConfigPathIn(dir)
	raw := []byte(`mode: server
data_dir: ` + dir + `
listen:
  address: 127.0.0.1
  port: 8743
backend:
  driver: local
auth:
  oidc:
    provider: github
`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadServer(dir)
	if err == nil || !strings.Contains(err.Error(), "auth.oidc") {
		t.Fatalf("want auth.oidc error, got %v", err)
	}
}

func TestTLSValidateACME(t *testing.T) {
	if err := (TLSConfig{Enabled: true, ACME: ACMEConfig{Enabled: true, Email: "a@b.c", Domains: []string{"x.com"}}}).Validate(); err != nil {
		t.Fatal(err)
	}
	err := (TLSConfig{Enabled: true, CertFile: "c", KeyFile: "k", ACME: ACMEConfig{Enabled: true, Email: "a@b.c", Domains: []string{"x.com"}}}).Validate()
	if err == nil {
		t.Fatal("expected mutual exclusion")
	}
	err = (TLSConfig{Enabled: false, ACME: ACMEConfig{Enabled: true}}).Validate()
	if err == nil {
		t.Fatal("expected tls.enabled required")
	}
}

func TestLoadServerACMEOK(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`mode: server
data_dir: ` + dir + `
listen:
  address: 127.0.0.1
  port: 8743
backend:
  driver: local
tls:
  enabled: true
  acme:
    enabled: true
    email: ops@example.com
    domains: ["context.example.com"]
`)
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TLS.ACME.Enabled || cfg.TLS.ACME.Email != "ops@example.com" {
		t.Fatalf("%+v", cfg.TLS.ACME)
	}
}
