package quotas

import (
	"fmt"
	"strconv"
	"strings"
)

// Config is storage quota limits (0 = use default).
type Config struct {
	MaxFileSize  int64 `yaml:"max_file_size"`  // bytes; default 5 MiB
	MaxSpaceSize int64 `yaml:"max_space_size"` // bytes; default 100 MiB
	MaxFiles     int   `yaml:"max_files"`      // default 5000
}

// Default returns shipped defaults from the server ops note.
func Default() Config {
	return Config{
		MaxFileSize:  5 << 20,
		MaxSpaceSize: 100 << 20,
		MaxFiles:     5000,
	}
}

func (c Config) withDefaults() Config {
	d := Default()
	if c.MaxFileSize <= 0 {
		c.MaxFileSize = d.MaxFileSize
	}
	if c.MaxSpaceSize <= 0 {
		c.MaxSpaceSize = d.MaxSpaceSize
	}
	if c.MaxFiles <= 0 {
		c.MaxFiles = d.MaxFiles
	}
	return c
}

// Exceeded is returned when a write would breach a quota.
type Exceeded struct {
	Quota string
	Used  int64
	Limit int64
	Msg   string
}

func (e *Exceeded) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("quota exceeded: %s used=%d limit=%d", e.Quota, e.Used, e.Limit)
}

// CheckFileSize rejects oversized single blobs.
func (c Config) CheckFileSize(n int64) error {
	c = c.withDefaults()
	if n > c.MaxFileSize {
		return &Exceeded{
			Quota: "max_file_size",
			Used:  n,
			Limit: c.MaxFileSize,
			Msg:   fmt.Sprintf("file too large: %d bytes (limit %d)", n, c.MaxFileSize),
		}
	}
	return nil
}

// CheckSpace rejects when adding deltaBytes / deltaFiles would exceed caps.
func (c Config) CheckSpace(currentBytes int64, currentFiles int, deltaBytes int64, deltaFiles int) error {
	c = c.withDefaults()
	if currentFiles+deltaFiles > c.MaxFiles {
		return &Exceeded{
			Quota: "max_files",
			Used:  int64(currentFiles + deltaFiles),
			Limit: int64(c.MaxFiles),
			Msg:   fmt.Sprintf("too many files: %d (limit %d)", currentFiles+deltaFiles, c.MaxFiles),
		}
	}
	if currentBytes+deltaBytes > c.MaxSpaceSize {
		return &Exceeded{
			Quota: "max_space_size",
			Used:  currentBytes + deltaBytes,
			Limit: c.MaxSpaceSize,
			Msg:   fmt.Sprintf("space too large: %d bytes (limit %d)", currentBytes+deltaBytes, c.MaxSpaceSize),
		}
	}
	return nil
}

// WarnFraction is the soft warning threshold (0.8).
const WarnFraction = 0.8

// NearLimit reports whether usage is at/above 80% of a quota.
func (c Config) NearLimit(currentBytes int64, currentFiles int) (quota string, used, limit int64, ok bool) {
	c = c.withDefaults()
	if float64(currentFiles) >= WarnFraction*float64(c.MaxFiles) {
		return "max_files", int64(currentFiles), int64(c.MaxFiles), true
	}
	if float64(currentBytes) >= WarnFraction*float64(c.MaxSpaceSize) {
		return "max_space_size", currentBytes, c.MaxSpaceSize, true
	}
	return "", 0, 0, false
}

// ParseSize parses "5 MB", "10MiB", "5242880".
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "kib"):
		mult = 1 << 10
		s = strings.TrimSpace(s[:len(s)-3])
	case strings.HasSuffix(s, "kb"):
		mult = 1000
		s = strings.TrimSpace(s[:len(s)-2])
	case strings.HasSuffix(s, "mib"):
		mult = 1 << 20
		s = strings.TrimSpace(s[:len(s)-3])
	case strings.HasSuffix(s, "mb"):
		mult = 1000 * 1000
		s = strings.TrimSpace(s[:len(s)-2])
	case strings.HasSuffix(s, "gib"):
		mult = 1 << 30
		s = strings.TrimSpace(s[:len(s)-3])
	case strings.HasSuffix(s, "gb"):
		mult = 1000 * 1000 * 1000
		s = strings.TrimSpace(s[:len(s)-2])
	case strings.HasSuffix(s, "b"):
		s = strings.TrimSpace(s[:len(s)-1])
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(n * float64(mult)), nil
}
