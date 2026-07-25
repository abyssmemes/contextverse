package server

import (
	"net/http"
	"testing"
)

func req(remote, xff string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/api/v1/spaces", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestForwardedForIgnoredFromUntrustedPeer(t *testing.T) {
	s := &Server{}
	got := s.clientIP(req("203.0.113.7:44321", "1.2.3.4"))
	if got != "203.0.113.7" {
		t.Fatalf("an untrusted peer must not be able to claim another address, got %q", got)
	}
}

func TestForwardedForHonouredFromTrustedProxy(t *testing.T) {
	proxies, bad := parseTrustedProxies([]string{"10.0.0.0/8", "192.168.1.5"})
	if len(bad) != 0 {
		t.Fatalf("unexpected parse errors: %v", bad)
	}
	s := &Server{proxies: proxies}
	if got := s.clientIP(req("10.1.2.3:5555", "1.2.3.4, 10.1.2.3")); got != "1.2.3.4" {
		t.Fatalf("trusted proxy header ignored, got %q", got)
	}
	if got := s.clientIP(req("192.168.1.5:5555", "9.9.9.9")); got != "9.9.9.9" {
		t.Fatalf("single-IP trust entry ignored, got %q", got)
	}
	if got := s.clientIP(req("192.168.1.6:5555", "9.9.9.9")); got != "192.168.1.6" {
		t.Fatalf("neighbour of a trusted proxy must not be trusted, got %q", got)
	}
}

func TestRateLimitKeyPrefersToken(t *testing.T) {
	s := &Server{}
	r := req("203.0.113.7:1", "")
	r.Header.Set("Authorization", "Bearer cv-kim-secret")
	if got := s.rateLimitKey(r); got != "bearer:cv-kim-secret" {
		t.Fatalf("token callers must be limited per token, got %q", got)
	}
	if got := s.rateLimitKey(req("203.0.113.7:1", "1.2.3.4")); got != "ip:203.0.113.7" {
		t.Fatalf("anonymous callers must be limited per real peer, got %q", got)
	}
}

func TestParseTrustedProxiesReportsGarbage(t *testing.T) {
	_, bad := parseTrustedProxies([]string{"", "not-an-ip", "10.0.0.0/8"})
	if len(bad) != 1 || bad[0] != "not-an-ip" {
		t.Fatalf("expected exactly the bad entry back, got %v", bad)
	}
}
