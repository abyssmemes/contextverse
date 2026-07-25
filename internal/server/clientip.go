package server

import (
	"net"
	"net/http"
	"strings"
)

// trustedProxies holds the peers whose X-Forwarded-For we believe. Empty means
// "trust nobody": the header is then ignored, because anyone can send it and it
// would otherwise let a caller forge the identity used for rate limiting and
// audit entries.
type trustedProxies struct {
	nets []*net.IPNet
}

func parseTrustedProxies(cidrs []string) (trustedProxies, []string) {
	var tp trustedProxies
	var bad []string
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(raw); err == nil {
			tp.nets = append(tp.nets, n)
			continue
		}
		if ip := net.ParseIP(raw); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			tp.nets = append(tp.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		bad = append(bad, raw)
	}
	return tp, bad
}

func (t trustedProxies) trusts(addr string) bool {
	if len(t.nets) == 0 {
		return false
	}
	ip := net.ParseIP(remoteIP(addr))
	if ip == nil {
		return false
	}
	for _, n := range t.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteIP strips the port from a RemoteAddr-style string.
func remoteIP(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// clientIP is the address we attribute the request to: the left-most forwarded
// address when the immediate peer is a trusted proxy, otherwise the peer itself.
func (s *Server) clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if s != nil && s.proxies.trusts(r.RemoteAddr) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
				return first
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}
	return remoteIP(r.RemoteAddr)
}

func (s *Server) rateLimitKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if tok != "" {
			return "bearer:" + tok
		}
	}
	return "ip:" + s.clientIP(r)
}
