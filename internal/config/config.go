package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Mode is the runtime deployment mode.
type Mode string

const (
	ModeSolo   Mode = "solo"
	ModeClient Mode = "client"
	ModeServer Mode = "server"
)

const (
	// DefaultSpaceDirName is the directory under the user home for the space.
	DefaultSpaceDirName = ".context"
	// ConfigFileName is the config filename inside the space root.
	ConfigFileName = "config.yaml"
	// ServerConfigPath is the conventional server config location.
	ServerConfigPath = "/srv/contextverse/config.yaml"
)

// Config is persisted at <space_root>/config.yaml.
type Config struct {
	Mode      Mode      `yaml:"mode"`
	SpaceRoot string    `yaml:"space_root"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
	Identity  Identity  `yaml:"identity"`
	Template  string    `yaml:"template,omitempty"`
	Backend   Backend   `yaml:"backend,omitempty"`
	Server    ClientServer `yaml:"server,omitempty"` // client mode
	Sync      SyncState    `yaml:"sync,omitempty"`   // client sync markers
}

// Backend selects the storage driver (local|git|s3|sql).
type Backend struct {
	Driver string `yaml:"driver,omitempty"` // local|git|s3|sql (default: local)

	// Git (driver=git)
	GitRemote   string `yaml:"git_remote,omitempty"`
	GitUser     string `yaml:"git_user,omitempty"`      // HTTPS username (often "git" or GitHub username)
	GitToken    string `yaml:"git_token,omitempty"`     // HTTPS PAT / password; prefer env CONTEXTVERSE_GIT_TOKEN
	GitSSHKey   string `yaml:"git_ssh_key,omitempty"`  // path to private key for SSH remotes
	GitAutoPush bool   `yaml:"git_auto_push,omitempty"` // push after each write (default true when remote set)

	// S3 (driver=s3) — works with AWS and MinIO / S3-compatible
	S3Endpoint  string `yaml:"s3_endpoint,omitempty"` // e.g. http://127.0.0.1:9000
	S3Region    string `yaml:"s3_region,omitempty"`
	S3Bucket    string `yaml:"s3_bucket,omitempty"`
	S3Prefix    string `yaml:"s3_prefix,omitempty"` // key prefix inside bucket
	S3AccessKey string `yaml:"s3_access_key,omitempty"`
	S3SecretKey string `yaml:"s3_secret_key,omitempty"`
	S3PathStyle bool   `yaml:"s3_path_style,omitempty"` // required for MinIO

	// SQL (driver=sql) — Postgres
	SQLDSN string `yaml:"sql_dsn,omitempty"` // postgres://user:pass@localhost:5432/contextverse?sslmode=disable
}

// Identity is collected during init.
type Identity struct {
	Name     string `yaml:"name"`
	Role     string `yaml:"role"`
	Language string `yaml:"language"`
}

// DefaultSpaceRoot returns ~/.context (expanded).
func DefaultSpaceRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, DefaultSpaceDirName), nil
}

// Path returns the config file path for a space root.
func Path(spaceRoot string) string {
	return filepath.Join(spaceRoot, ConfigFileName)
}

// Load reads config from spaceRoot/config.yaml.
func Load(spaceRoot string) (*Config, error) {
	path := Path(spaceRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.SpaceRoot == "" {
		cfg.SpaceRoot = spaceRoot
	}
	return &cfg, nil
}

// Save writes config atomically.
func Save(cfg *Config) error {
	if cfg.SpaceRoot == "" {
		return fmt.Errorf("space_root is empty")
	}
	cfg.UpdatedAt = time.Now().UTC()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = cfg.UpdatedAt
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	path := Path(cfg.SpaceRoot)
	if err := os.MkdirAll(cfg.SpaceRoot, 0o755); err != nil {
		return fmt.Errorf("create space root: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write config temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// DetectMode inspects conventional locations and returns the active mode.
func DetectMode() Mode {
	if _, err := os.Stat(ServerConfigPath); err == nil {
		return ModeServer
	}
	root, err := DefaultSpaceRoot()
	if err != nil {
		return ModeSolo
	}
	cfg, err := Load(root)
	if err != nil {
		return ModeSolo
	}
	if cfg.Mode != "" {
		return cfg.Mode
	}
	return ModeSolo
}

// Exists reports whether a config is present at spaceRoot.
func Exists(spaceRoot string) bool {
	_, err := os.Stat(Path(spaceRoot))
	return err == nil
}
