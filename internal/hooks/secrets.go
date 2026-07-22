package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Finding is one suspected secret match.
type Finding struct {
	Path    string `json:"path"`
	Rule    string `json:"rule"`
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet,omitempty"` // redacted
}

func (f Finding) Error() string {
	return fmt.Sprintf("secret-scan: %s matched %s (line %d)", f.Path, f.Rule, f.Line)
}

// BlockedError is returned when secret-scan rejects a write.
type BlockedError struct {
	Findings []Finding
}

func (e *BlockedError) Error() string {
	if len(e.Findings) == 0 {
		return "secret-scan blocked write"
	}
	return e.Findings[0].Error()
}

// Config controls server-side hooks.
type Config struct {
	SecretScan SecretScanConfig `yaml:"secret_scan"`
}

// SecretScanConfig is the block-secrets guardrail.
type SecretScanConfig struct {
	Enabled     bool `yaml:"enabled"`
	OnViolation string `yaml:"on_violation"` // block (default) | warn
}

// Load reads <dataDir>/hooks.yaml (missing → enabled block defaults).
func Load(dataDir string) (Config, error) {
	cfg := Config{
		SecretScan: SecretScanConfig{Enabled: true, OnViolation: "block"},
	}
	path := filepath.Join(dataDir, "hooks.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.SecretScan.OnViolation == "" {
		cfg.SecretScan.OnViolation = "block"
	}
	return cfg, nil
}

type rule struct {
	name string
	re   *regexp.Regexp
}

var defaultRules = []rule{
	{"aws_access_key_id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"github_pat", regexp.MustCompile(`ghp_[A-Za-z0-9_]{20,}`)},
	{"github_fine_grained", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"slack_token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"private_key_header", regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----`)},
	{"generic_api_key_assign", regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token)\s*[:=]\s*['"]?[A-Za-z0-9_\-]{20,}`)},
}

// ScanBytes runs default secret patterns against content.
func ScanBytes(path string, data []byte) []Finding {
	var out []Finding
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		for _, r := range defaultRules {
			if loc := r.re.FindStringIndex(line); loc != nil {
				out = append(out, Finding{
					Path:    path,
					Rule:    r.name,
					Line:    i + 1,
					Snippet: redact(line, loc[0], loc[1]),
				})
			}
		}
	}
	return out
}

func redact(line string, start, end int) string {
	if start < 0 || end > len(line) || start >= end {
		return "(redacted)"
	}
	prefix := line[:start]
	if len(prefix) > 24 {
		prefix = "…" + prefix[len(prefix)-24:]
	}
	return prefix + "***"
}

// CheckPut returns BlockedError when enabled and findings exist.
func (c Config) CheckPut(path string, data []byte) error {
	if !c.SecretScan.Enabled {
		return nil
	}
	findings := ScanBytes(path, data)
	if len(findings) == 0 {
		return nil
	}
	if strings.EqualFold(c.SecretScan.OnViolation, "warn") {
		return nil // warn-only: caller may log; do not block
	}
	return &BlockedError{Findings: findings}
}

// CheckPuts scans multiple put ops; returns first block.
func (c Config) CheckPuts(paths []string, bodies [][]byte) error {
	for i := range paths {
		if err := c.CheckPut(paths[i], bodies[i]); err != nil {
			return err
		}
	}
	return nil
}
