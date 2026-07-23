package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
	"golang.org/x/crypto/acme/autocert"
)

const (
	ChallengeHTTP01 = "http-01"
	ChallengeDNS01  = "dns-01"
	ProviderCloudflare = "cloudflare"
)

// Config is Let's Encrypt (ACME) settings for the OSS server.
type Config struct {
	Enabled   bool     `yaml:"enabled"`
	Email     string   `yaml:"email"`
	Domains   []string `yaml:"domains"`
	CacheDir  string   `yaml:"cache_dir,omitempty"`
	HTTPAddr  string   `yaml:"http_addr,omitempty"` // default :80 for HTTP-01
	Challenge string   `yaml:"challenge,omitempty"` // http-01 (default) | dns-01
	DNS       DNSConfig `yaml:"dns,omitempty"`
}

// DNSConfig selects the DNS-01 provider (Cloudflare first).
type DNSConfig struct {
	Provider string `yaml:"provider,omitempty"` // cloudflare
}

// Validate checks ACME knobs (call after resolving cache dir).
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Email) == "" {
		return fmt.Errorf("tls.acme.email is required when acme.enabled")
	}
	if len(c.Domains) == 0 {
		return fmt.Errorf("tls.acme.domains must list at least one hostname")
	}
	for _, d := range c.Domains {
		if strings.TrimSpace(d) == "" {
			return fmt.Errorf("tls.acme.domains contains an empty entry")
		}
	}
	ch := c.NormalizedChallenge()
	switch ch {
	case ChallengeHTTP01:
		return nil
	case ChallengeDNS01:
		p := strings.ToLower(strings.TrimSpace(c.DNS.Provider))
		if p == "" {
			p = ProviderCloudflare
		}
		if p != ProviderCloudflare {
			return fmt.Errorf("tls.acme.dns.provider %q is not supported (want cloudflare)", c.DNS.Provider)
		}
		return nil
	default:
		return fmt.Errorf("tls.acme.challenge must be http-01 or dns-01 (got %q)", c.Challenge)
	}
}

// NormalizedChallenge returns http-01 when unset.
func (c Config) NormalizedChallenge() string {
	ch := strings.ToLower(strings.TrimSpace(c.Challenge))
	if ch == "" {
		return ChallengeHTTP01
	}
	return ch
}

// ResolveCacheDir returns cache_dir or <dataDir>/tls/acme.
func ResolveCacheDir(dataDir, cacheDir string) string {
	if strings.TrimSpace(cacheDir) != "" {
		return cacheDir
	}
	return filepath.Join(dataDir, "tls", "acme")
}

// Manager wraps autocert (HTTP-01) or lego (DNS-01).
type Manager struct {
	Inner *autocert.Manager // HTTP-01 / TLS-ALPN-01
	Cfg   Config
	cache string

	mu       sync.RWMutex
	dnsCert  *tls.Certificate
	stopRenew chan struct{}
}

// New builds a Manager. Caller must ensure cache dir is writable.
// For DNS-01, obtains (or loads) certificates via lego before returning.
func New(cfg Config, cacheDir string) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}
	hosts := make([]string, 0, len(cfg.Domains))
	for _, d := range cfg.Domains {
		hosts = append(hosts, strings.TrimSpace(d))
	}
	m := &Manager{Cfg: cfg, cache: cacheDir, stopRenew: make(chan struct{})}

	switch cfg.NormalizedChallenge() {
	case ChallengeDNS01:
		if err := m.ensureDNSCert(hosts); err != nil {
			return nil, err
		}
		go m.renewLoop(hosts)
		return m, nil
	default:
		m.Inner = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Email:      cfg.Email,
			HostPolicy: autocert.HostWhitelist(hosts...),
			Cache:      autocert.DirCache(cacheDir),
		}
		return m, nil
	}
}

// Close stops background DNS renewals.
func (m *Manager) Close() {
	if m == nil || m.stopRenew == nil {
		return
	}
	select {
	case <-m.stopRenew:
	default:
		close(m.stopRenew)
	}
}

// TLSConfig returns a tls.Config that obtains certificates via ACME.
func (m *Manager) TLSConfig() *tls.Config {
	if m == nil {
		return nil
	}
	if m.Cfg.NormalizedChallenge() == ChallengeDNS01 {
		return &tls.Config{
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2", "http/1.1"},
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				m.mu.RLock()
				defer m.mu.RUnlock()
				if m.dnsCert == nil {
					return nil, fmt.Errorf("acme dns-01: no certificate loaded")
				}
				return m.dnsCert, nil
			},
		}
	}
	if m.Inner == nil {
		return nil
	}
	return m.Inner.TLSConfig()
}

// ChallengeHTTPAddr returns the bind address for the HTTP-01 helper listener.
// Empty for DNS-01 (no HTTP challenge listener).
func (m *Manager) ChallengeHTTPAddr() string {
	if m == nil || m.Cfg.NormalizedChallenge() == ChallengeDNS01 {
		return ""
	}
	if m.Cfg.HTTPAddr != "" {
		return m.Cfg.HTTPAddr
	}
	return ":80"
}

// HTTPHandler serves ACME HTTP-01 challenges (and 404 otherwise).
func (m *Manager) HTTPHandler() http.Handler {
	if m == nil || m.Inner == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return m.Inner.HTTPHandler(nil)
}

// StatusLine is a short human summary for CLI.
func (m *Manager) StatusLine(cacheDir string) string {
	if m == nil || !m.Cfg.Enabled {
		return "acme: disabled"
	}
	ch := m.Cfg.NormalizedChallenge()
	if ch == ChallengeDNS01 {
		p := m.Cfg.DNS.Provider
		if p == "" {
			p = ProviderCloudflare
		}
		return fmt.Sprintf("acme: enabled challenge=dns-01 provider=%s email=%s domains=%s cache=%s",
			p, m.Cfg.Email, strings.Join(m.Cfg.Domains, ","), cacheDir)
	}
	return fmt.Sprintf("acme: enabled challenge=http-01 email=%s domains=%s cache=%s http_challenge=%s",
		m.Cfg.Email, strings.Join(m.Cfg.Domains, ","), cacheDir, m.ChallengeHTTPAddr())
}

func (m *Manager) certPaths() (certPath, keyPath string) {
	return filepath.Join(m.cache, "cert.pem"), filepath.Join(m.cache, "key.pem")
}

func (m *Manager) ensureDNSCert(domains []string) error {
	certPath, keyPath := m.certPaths()
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil {
				m.mu.Lock()
				m.dnsCert = &cert
				m.mu.Unlock()
				if !needsRenew(&cert) {
					return nil
				}
			}
		}
	}
	return m.obtainDNS(domains)
}

func needsRenew(cert *tls.Certificate) bool {
	if cert == nil || len(cert.Certificate) == 0 {
		return true
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return true
	}
	return time.Until(parsed.NotAfter) < 30*24*time.Hour
}

func (m *Manager) renewLoop(domains []string) {
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-m.stopRenew:
			return
		case <-t.C:
			m.mu.RLock()
			c := m.dnsCert
			m.mu.RUnlock()
			if needsRenew(c) {
				_ = m.obtainDNS(domains)
			}
		}
	}
}

func (m *Manager) obtainDNS(domains []string) error {
	user, err := loadOrCreateUser(m.cache, m.Cfg.Email)
	if err != nil {
		return err
	}
	config := lego.NewConfig(user)
	config.Certificate.KeyType = certcrypto.EC256
	client, err := lego.NewClient(config)
	if err != nil {
		return fmt.Errorf("acme dns-01 lego client: %w", err)
	}
	provider, err := cloudflare.NewDNSProvider()
	if err != nil {
		return fmt.Errorf("acme dns-01 cloudflare provider (set CLOUDFLARE_DNS_API_TOKEN or CF_DNS_API_TOKEN): %w", err)
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return err
	}
	if user.Registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return fmt.Errorf("acme dns-01 register: %w", err)
		}
		user.Registration = reg
		if err := saveUser(m.cache, user); err != nil {
			return err
		}
	}
	req := certificate.ObtainRequest{Domains: domains, Bundle: true}
	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return fmt.Errorf("acme dns-01 obtain: %w", err)
	}
	certPath, keyPath := m.certPaths()
	if err := os.WriteFile(certPath, res.Certificate, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, res.PrivateKey, 0o600); err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(res.Certificate, res.PrivateKey)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.dnsCert = &cert
	m.mu.Unlock()
	return nil
}

type acmeUser struct {
	Email        string                 `json:"email"`
	Registration *registration.Resource `json:"registration,omitempty"`
	key          crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.Email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.Registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

func loadOrCreateUser(cache, email string) (*acmeUser, error) {
	userPath := filepath.Join(cache, "account.json")
	keyPath := filepath.Join(cache, "account.key")
	if raw, err := os.ReadFile(userPath); err == nil {
		var u acmeUser
		if err := json.Unmarshal(raw, &u); err != nil {
			return nil, err
		}
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(keyPEM)
		if block == nil {
			return nil, fmt.Errorf("invalid account.key")
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		u.key = key
		u.Email = email
		return &u, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	u := &acmeUser{Email: email, key: key}
	if err := saveUser(cache, u); err != nil {
		return nil, err
	}
	return u, nil
}

func saveUser(cache string, u *acmeUser) error {
	raw, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cache, "account.json"), raw, 0o600); err != nil {
		return err
	}
	ec, ok := u.key.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("unexpected account key type")
	}
	der, err := x509.MarshalECPrivateKey(ec)
	if err != nil {
		return err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return os.WriteFile(filepath.Join(cache, "account.key"), pemBytes, 0o600)
}
