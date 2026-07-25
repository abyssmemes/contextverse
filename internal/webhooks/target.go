package webhooks

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// A webhook URL is attacker-chosen: whoever can configure a hook makes this
// process issue a signed POST to an address of their choosing. On a pooled cloud
// node that reaches the orchestrator API, the metadata service and every other
// tenant's port, so destinations are filtered — by default to public addresses
// only, and always with the cloud metadata endpoints refused.
//
// The check runs twice on purpose: once when a hook is saved, for a clear error,
// and again on the socket just before connect, because DNS can answer
// differently the second time (rebinding).

// ErrUnsafeTarget is returned for a destination the policy refuses.
var ErrUnsafeTarget = fmt.Errorf("unsafe webhook target")

// TargetPolicy decides which destinations may be dialled.
type TargetPolicy struct {
	// AllowPrivate permits loopback, RFC1918 and other non-public addresses.
	// Self-hosted servers delivering to an internal relay need this; a
	// multi-tenant node must not have it.
	AllowPrivate bool
}

// metadataHosts are never legitimate webhook destinations: they hand out cloud
// instance credentials to anything that can issue a plain HTTP GET.
var metadataHosts = map[string]bool{
	"169.254.169.254":          true,
	"metadata.google.internal": true,
	"metadata.goog":            true,
	"instance-data":            true,
	"fd00:ec2::254":            true,
	"metadata.azure.com":       true,
	"169.254.170.2":            true, // ECS task credentials
}

// ValidateURL checks the scheme and the literal host of a configured hook.
func (p TargetPolicy) ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeTarget, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%w: scheme %q is not http(s)", ErrUnsafeTarget, u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrUnsafeTarget)
	}
	if metadataHosts[host] {
		return fmt.Errorf("%w: %s is a cloud metadata endpoint", ErrUnsafeTarget, host)
	}
	if ip := net.ParseIP(host); ip != nil {
		return p.checkIP(ip)
	}
	if !p.AllowPrivate && localName(host) {
		return fmt.Errorf("%w: %s always resolves locally (set webhooks.allow_private_targets to permit internal destinations)",
			ErrUnsafeTarget, host)
	}
	// Any other name resolves at dial time; that is where the address is checked.
	return nil
}

// localName covers the names RFC 6761 reserves for the local host or network:
// they can never be a legitimate destination under the strict policy.
func localName(host string) bool {
	return host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal")
}

func (p TargetPolicy) checkIP(ip net.IP) error {
	if metadataIP(ip) {
		return fmt.Errorf("%w: %s is a cloud metadata endpoint", ErrUnsafeTarget, ip)
	}
	if p.AllowPrivate {
		return nil
	}
	if !isPublic(ip) {
		return fmt.Errorf("%w: %s is not a public address (set webhooks.allow_private_targets to permit internal destinations)",
			ErrUnsafeTarget, ip)
	}
	return nil
}

func metadataIP(ip net.IP) bool {
	return ip.Equal(net.IPv4(169, 254, 169, 254)) || ip.Equal(net.IPv4(169, 254, 170, 2))
}

func isPublic(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // CGNAT 100.64/10
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // IETF protocol assignments
			return false
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19): // benchmarking
			return false
		case v4[0] >= 240: // reserved
			return false
		}
		return true
	}
	// IPv6: refuse unique-local and anything mapping onto a rejected v4 address.
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return true
}

// Client builds an HTTP client that enforces the policy on the socket and does
// not follow redirects — a 302 is the easy way around a host check.
func (p TargetPolicy) Client(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrUnsafeTarget, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("%w: %s is not an address", ErrUnsafeTarget, host)
			}
			return p.checkIP(ip)
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConnsPerHost:   2,
			DisableCompression:    true,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
