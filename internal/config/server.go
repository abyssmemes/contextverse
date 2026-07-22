package config

import (
	"fmt"
	"os"
	"path/filepath"
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

	Listen ListenConfig `yaml:"listen"`
	TLS    TLSConfig    `yaml:"tls"`
	Backend Backend     `yaml:"backend"`
	Defaults ServerDefaults `yaml:"defaults"`
	Auth   ServerAuth   `yaml:"auth"`
}

// ListenConfig is bind address/port.
type ListenConfig struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

// TLSConfig optional TLS (disabled in 2a by default).
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty"`
}

// ServerDefaults holds init defaults.
type ServerDefaults struct {
	Space string `yaml:"space"`
}

// ServerAuth holds auth policy knobs.
type ServerAuth struct {
	TokenTTLDays int `yaml:"token_ttl"` // 0 = no expiry in 2a
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
	return &cfg, nil
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
