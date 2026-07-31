package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/orkcom-tech/contextverse/internal/logx"
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
	Mode      Mode         `yaml:"mode"`
	SpaceRoot string       `yaml:"space_root"`
	CreatedAt time.Time    `yaml:"created_at"`
	UpdatedAt time.Time    `yaml:"updated_at"`
	Identity  Identity     `yaml:"identity"`
	Template  string       `yaml:"template,omitempty"`
	Backend   Backend      `yaml:"backend,omitempty"`
	Server    ClientServer `yaml:"server,omitempty"` // client mode
	Sync      SyncState    `yaml:"sync,omitempty"`   // client sync markers
	Daemon    DaemonConfig `yaml:"daemon,omitempty"` // client background poller
	Editor    string       `yaml:"editor,omitempty"` // remembered TUI editor choice (binary id)

	// Anchors record where each project's code actually lives, learned from the
	// directory `contextd activate` was run in.
	//
	// Without this the space can describe a project but has no idea where it is,
	// so a document that mentions ./scripts/deploy.sh cannot be checked against
	// anything. It is what lets the context graph reach past prose into code.
	Anchors []Anchor `yaml:"anchors,omitempty"`
}

// Anchor ties a project in the space to a working directory on this machine.
//
// Deliberately per-machine: a checkout path is local truth, not something to
// push to teammates whose repositories live somewhere else entirely.
type Anchor struct {
	Project  string    `yaml:"project"`
	Path     string    `yaml:"path"`
	LastSeen time.Time `yaml:"last_seen"`
}

// RecordAnchor remembers that a project was activated in dir, replacing any
// previous path for that project. Reports whether anything changed, so callers
// can skip a config write on the common case of activating in the same place.
func (c *Config) RecordAnchor(project, dir string, now time.Time) bool {
	if project == "" || dir == "" {
		return false
	}
	for i := range c.Anchors {
		if c.Anchors[i].Project != project {
			continue
		}
		// A moved checkout should follow the project, not accumulate as a second
		// entry that leaves the graph guessing which one is live.
		moved := c.Anchors[i].Path != dir
		c.Anchors[i].Path = dir
		c.Anchors[i].LastSeen = now
		return moved
	}
	c.Anchors = append(c.Anchors, Anchor{Project: project, Path: dir, LastSeen: now})
	return true
}

// AnchorFor returns the recorded path for a project.
func (c *Config) AnchorFor(project string) (string, bool) {
	for _, a := range c.Anchors {
		if a.Project == project {
			return a.Path, true
		}
	}
	return "", false
}

// DaemonConfig controls contextd daemon (client head poll → pull).
type DaemonConfig struct {
	IntervalSec int `yaml:"interval_sec,omitempty"` // default 60
}

// Backend selects the storage driver (local|git|s3|sql).
type Backend struct {
	Driver string `yaml:"driver,omitempty"` // local|git|s3|sql (default: local)

	// Git (driver=git)
	GitRemote   string `yaml:"git_remote,omitempty"`
	GitUser     string `yaml:"git_user,omitempty"`      // HTTPS username (often "git" or GitHub username)
	GitToken    string `yaml:"git_token,omitempty"`     // HTTPS PAT / password; prefer env CONTEXTVERSE_GIT_TOKEN
	GitSSHKey   string `yaml:"git_ssh_key,omitempty"`   // path to private key for SSH remotes
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
	// Repair on read, not only on write: a config written by an earlier version
	// is world-readable and holds credentials, and nobody is going to notice
	// that on their own. Best-effort — an unwritable config is still usable.
	if err := restrictSecretFile(path); err != nil {
		logx.L().Debug("could not tighten config permissions", "path", path, "err", err)
	}

	// Where we just read from is authoritative, and it is stored absolute.
	//
	// The recorded space_root was whatever string --dir happened to carry at
	// init, so a relative one made Save resolve against the *calling* working
	// directory: running `contextd activate` from a project wrote a stray
	// config.yaml under that project and left the real one untouched. In client
	// mode that silently dropped Sync.LastHead, so the client re-pulled
	// everything every time and pushed against a stale head.
	if abs, err := filepath.Abs(spaceRoot); err == nil {
		cfg.SpaceRoot = abs
	} else if cfg.SpaceRoot == "" {
		cfg.SpaceRoot = spaceRoot
	}
	return &cfg, nil
}

// Save writes config atomically.
func Save(cfg *Config) error {
	if cfg.SpaceRoot == "" {
		return fmt.Errorf("space_root is empty")
	}
	// Belt and braces for configs built in memory rather than loaded: a relative
	// root here would write wherever the caller happens to be standing.
	if abs, err := filepath.Abs(cfg.SpaceRoot); err == nil {
		cfg.SpaceRoot = abs
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
	if err := os.WriteFile(tmp, data, secretFileMode); err != nil {
		return fmt.Errorf("write config temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}
	// A file written before this was tightened keeps its old mode through a
	// rename, so the permissions are asserted rather than assumed.
	return restrictSecretFile(path)
}

// secretFileMode is owner-only. These files carry git_token, s3_secret_key and
// sql_dsn — a DSN with the password inline — and were world-readable at 0644,
// while the bearer token next to them was already 0600. On a shared host or in
// a container with a second user that is a credential handed out for free.
const secretFileMode os.FileMode = 0o600

// restrictSecretFile removes group and world access from a file that holds
// credentials, including one written by an older version.
func restrictSecretFile(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 == 0 {
		return nil
	}
	return os.Chmod(path, secretFileMode)
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
