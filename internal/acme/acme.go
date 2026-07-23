package acme

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/acme/autocert"
)

// Config is Let's Encrypt (ACME) settings for the OSS server.
type Config struct {
	Enabled  bool     `yaml:"enabled"`
	Email    string   `yaml:"email"`
	Domains  []string `yaml:"domains"`
	CacheDir string   `yaml:"cache_dir,omitempty"`
	// HTTPAddr is where HTTP-01 challenges are served when the main listen port is not 443.
	// Default ":80". Set empty to disable the challenge listener (TLS-ALPN-01 on :443 only).
	HTTPAddr string `yaml:"http_addr,omitempty"`
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
	return nil
}

// ResolveCacheDir returns cache_dir or <dataDir>/tls/acme.
func ResolveCacheDir(dataDir, cacheDir string) string {
	if strings.TrimSpace(cacheDir) != "" {
		return cacheDir
	}
	return filepath.Join(dataDir, "tls", "acme")
}

// Manager wraps autocert for HTTP-01 / TLS-ALPN-01.
type Manager struct {
	Inner *autocert.Manager
	Cfg   Config
}

// New builds a Manager. Caller must ensure cache dir is writable.
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
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      cfg.Email,
		HostPolicy: autocert.HostWhitelist(hosts...),
		Cache:      autocert.DirCache(cacheDir),
	}
	return &Manager{Inner: m, Cfg: cfg}, nil
}

// TLSConfig returns a tls.Config that obtains certificates via ACME.
func (m *Manager) TLSConfig() *tls.Config {
	if m == nil || m.Inner == nil {
		return nil
	}
	return m.Inner.TLSConfig()
}

// ChallengeHTTPAddr returns the bind address for the HTTP-01 helper listener.
func (m *Manager) ChallengeHTTPAddr() string {
	if m == nil {
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
	return fmt.Sprintf("acme: enabled email=%s domains=%s cache=%s http_challenge=%s",
		m.Cfg.Email, strings.Join(m.Cfg.Domains, ","), cacheDir, m.ChallengeHTTPAddr())
}
