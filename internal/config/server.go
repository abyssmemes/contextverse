package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultServerDirName is used when /srv/contextverse is not writable.
	DefaultServerDirName = ".contextverse-server"
	// DefaultListenAddr for Phase 2a.
	DefaultListenAddr = "127.0.0.1"
	// DefaultListenPort for Phase 2a.
	DefaultListenPort = 8743
)

// ServerConfig is persisted at <data_dir>/config.yaml for mode=server.
type ServerConfig struct {
	Mode      Mode      `yaml:"mode"`
	DataDir   string    `yaml:"data_dir"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`

	Listen    ListenConfig    `yaml:"listen"`
	TLS       TLSConfig       `yaml:"tls"`
	Backend   Backend         `yaml:"backend"`
	Defaults  ServerDefaults  `yaml:"defaults"`
	Auth      ServerAuth      `yaml:"auth"`
	TUI       TUIConfig       `yaml:"tui,omitempty"`
	RateLimit RateLimitConfig `yaml:"rate_limit,omitempty"`
	Quotas    QuotasConfig    `yaml:"quotas,omitempty"`
	Tracing   TracingConfig   `yaml:"tracing,omitempty"`
}

// TracingConfig is optional OpenTelemetry export (off by default).
type TracingConfig struct {
	OTLPEndpoint string `yaml:"otlp_endpoint,omitempty"` // e.g. http://localhost:4318 — empty = disabled
}

// RateLimitConfig is in-process API throttling.
type RateLimitConfig struct {
	Enabled           bool `yaml:"enabled"`
	RequestsPerMinute int  `yaml:"requests_per_minute"`
	AuthPerMinute     int  `yaml:"auth_per_minute"`
	explicit          bool `yaml:"-"` // true when rate_limit: appeared in YAML
}

// UnmarshalYAML records that the block was present (so enabled: false can opt out).
func (c *RateLimitConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw RateLimitConfig
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*c = RateLimitConfig(r)
	c.explicit = true
	return nil
}

// QuotasConfig limits space growth (0 = default).
type QuotasConfig struct {
	MaxFileSize  int64 `yaml:"max_file_size"`
	MaxSpaceSize int64 `yaml:"max_space_size"`
	MaxFiles     int   `yaml:"max_files"`
}

// TUIConfig holds server TUI / Wish SSH options (Model B).
type TUIConfig struct {
	SSH TUISSHConfig `yaml:"ssh"`
}

// TUISSHConfig is contextd's own SSH listener for the admin TUI.
type TUISSHConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Listen     string `yaml:"listen"`      // default 127.0.0.1:2222
	AutoLaunch bool   `yaml:"auto_launch"` // reserved; MVP always launches TUI
}

// ListenConfig is bind address/port.
type ListenConfig struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

// TLSConfig optional TLS (disabled by default).
type TLSConfig struct {
	Enabled  bool       `yaml:"enabled"`
	CertFile string     `yaml:"cert_file,omitempty"`
	KeyFile  string     `yaml:"key_file,omitempty"`
	ACME     ACMEConfig `yaml:"acme,omitempty"`
}

// ACMEConfig is Let's Encrypt (OSS). Mutual exclusion with cert_file/key_file.
type ACMEConfig struct {
	Enabled   bool          `yaml:"enabled"`
	Email     string        `yaml:"email"`
	Domains   []string      `yaml:"domains"`
	CacheDir  string        `yaml:"cache_dir,omitempty"`
	HTTPAddr  string        `yaml:"http_addr,omitempty"`  // default :80 for HTTP-01
	Challenge string        `yaml:"challenge,omitempty"` // http-01 (default) | dns-01
	DNS       ACMEDNSConfig `yaml:"dns,omitempty"`
}

// ACMEDNSConfig selects the DNS-01 provider.
type ACMEDNSConfig struct {
	Provider string `yaml:"provider,omitempty"` // cloudflare
}

// ServerDefaults holds init defaults.
type ServerDefaults struct {
	Space string `yaml:"space"`
}

// ServerAuth holds auth policy knobs.
// OIDC/MFA keys are rejected on OSS LoadServer (cloud control plane only).
type ServerAuth struct {
	TokenTTLDays int `yaml:"token_ttl"` // 0 = no expiry in 2a
	OIDC         any `yaml:"oidc,omitempty"`
	MFA          any `yaml:"mfa,omitempty"`
}

// ClientServer points a client checkout at a remote.
type ClientServer struct {
	URL       string `yaml:"url"`
	Space     string `yaml:"space"`
	TokenFile string `yaml:"token_file,omitempty"`
}

// SyncState is persisted client sync markers (also in .sync/state.yaml optionally).
type SyncState struct {
	LastHead   string    `yaml:"last_head,omitempty"`
	LastSyncAt time.Time `yaml:"last_sync_at,omitempty"`
}

// DefaultServerDataDir returns ~/.contextverse-server.
func DefaultServerDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DefaultServerDirName), nil
}

// ServerConfigPath returns <dataDir>/config.yaml.
func ServerConfigPathIn(dataDir string) string {
	return filepath.Join(dataDir, ConfigFileName)
}

// LoadServer reads server config from dataDir.
func LoadServer(dataDir string) (*ServerConfig, error) {
	path := ServerConfigPathIn(dataDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read server config %s: %w", path, err)
	}
	var cfg ServerConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse server config: %w", err)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = dataDir
	}
	if cfg.Listen.Address == "" {
		cfg.Listen.Address = DefaultListenAddr
	}
	if cfg.Listen.Port == 0 {
		cfg.Listen.Port = DefaultListenPort
	}
	if cfg.Backend.Driver == "" {
		cfg.Backend.Driver = "local"
	}
	if cfg.TUI.SSH.Listen == "" {
		cfg.TUI.SSH.Listen = DefaultTUISSHListen
	}
	if !cfg.RateLimit.explicit {
		cfg.RateLimit.Enabled = true
		cfg.RateLimit.RequestsPerMinute = 120
		cfg.RateLimit.AuthPerMinute = 10
		cfg.RateLimit.explicit = true
	} else {
		if cfg.RateLimit.RequestsPerMinute == 0 {
			cfg.RateLimit.RequestsPerMinute = 120
		}
		if cfg.RateLimit.AuthPerMinute == 0 {
			cfg.RateLimit.AuthPerMinute = 10
		}
	}
	if cfg.Quotas.MaxFileSize == 0 && cfg.Quotas.MaxSpaceSize == 0 && cfg.Quotas.MaxFiles == 0 {
		cfg.Quotas.MaxFileSize = 5 << 20
		cfg.Quotas.MaxSpaceSize = 100 << 20
		cfg.Quotas.MaxFiles = 5000
	}
	if err := cfg.Auth.RejectCloudOnly(); err != nil {
		return nil, err
	}
	if err := cfg.TLS.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// RejectCloudOnly fails if cloud-only auth blocks appear in OSS config.
func (a ServerAuth) RejectCloudOnly() error {
	if a.OIDC != nil {
		return fmt.Errorf("auth.oidc is a ContextVerse Cloud control-plane feature (SSO); remove it from server config — see docs/planning/contextverse-auth-cloud-modules.md")
	}
	if a.MFA != nil {
		return fmt.Errorf("auth.mfa is a ContextVerse Cloud control-plane feature; remove it from server config — see docs/planning/contextverse-auth-cloud-modules.md")
	}
	return nil
}

// Validate checks TLS static vs ACME mutual exclusion.
func (t TLSConfig) Validate() error {
	if t.ACME.Enabled && !t.Enabled {
		return fmt.Errorf("tls.acme.enabled requires tls.enabled")
	}
	if !t.Enabled {
		return nil
	}
	if t.ACME.Enabled {
		if t.CertFile != "" || t.KeyFile != "" {
			return fmt.Errorf("tls.acme.enabled cannot be combined with tls.cert_file/key_file")
		}
		if t.ACME.Email == "" {
			return fmt.Errorf("tls.acme.email is required when tls.acme.enabled")
		}
		if len(t.ACME.Domains) == 0 {
			return fmt.Errorf("tls.acme.domains is required when tls.acme.enabled")
		}
		ch := strings.ToLower(strings.TrimSpace(t.ACME.Challenge))
		if ch == "" {
			ch = "http-01"
		}
		switch ch {
		case "http-01":
			return nil
		case "dns-01":
			p := strings.ToLower(strings.TrimSpace(t.ACME.DNS.Provider))
			if p == "" {
				p = "cloudflare"
			}
			if p != "cloudflare" {
				return fmt.Errorf("tls.acme.dns.provider %q is not supported (want cloudflare)", t.ACME.DNS.Provider)
			}
			return nil
		default:
			return fmt.Errorf("tls.acme.challenge must be http-01 or dns-01 (got %q)", t.ACME.Challenge)
		}
	}
	if t.CertFile == "" || t.KeyFile == "" {
		return fmt.Errorf("tls.enabled requires tls.cert_file and tls.key_file (or tls.acme.enabled)")
	}
	return nil
}

const (
	// DefaultTUISSHListen is the Model B Wish bind address.
	DefaultTUISSHListen = "127.0.0.1:2222"
)

// TUISSHHostKeyPath returns <dataDir>/auth/tui_host_ed25519.
func TUISSHHostKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "tui_host_ed25519")
}

// TUISSHAuthorizedKeysPath returns <dataDir>/auth/tui_authorized_keys.
func TUISSHAuthorizedKeysPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "tui_authorized_keys")
}

// SaveServer writes server config atomically.
func SaveServer(cfg *ServerConfig) error {
	if cfg.DataDir == "" {
		return fmt.Errorf("data_dir is empty")
	}
	cfg.Mode = ModeServer
	cfg.UpdatedAt = time.Now().UTC()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = cfg.UpdatedAt
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	path := ServerConfigPathIn(cfg.DataDir)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ServerExists reports whether server config is present.
func ServerExists(dataDir string) bool {
	_, err := os.Stat(ServerConfigPathIn(dataDir))
	return err == nil
}

// Addr returns host:port.
func (c *ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Listen.Address, c.Listen.Port)
}

// BaseURL returns http(s)://addr for local probes.
func (c *ServerConfig) BaseURL() string {
	scheme := "http"
	if c.TLS.Enabled {
		scheme = "https"
	}
	host := c.Listen.Address
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, c.Listen.Port)
}
