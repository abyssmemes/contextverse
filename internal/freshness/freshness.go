package freshness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Meta is freshness fields from YAML frontmatter (or derived).
type Meta struct {
	Path          string
	LastValidated time.Time
	StaleAfter    time.Duration
	Owner         string
	Confidence    string
	Stale         bool
	Source        string // frontmatter|mtime
}

// ScanDir walks a space root for .md files with stale-after and reports stale ones.
func ScanDir(root string, now time.Time) ([]Meta, error) {
	var out []Meta
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == ".contextverse" || base == ".sync" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m, ok := Parse(rel, raw, info.ModTime())
		if !ok {
			return nil
		}
		if m.StaleAfter <= 0 {
			return nil
		}
		m.Stale = now.After(m.LastValidated.Add(m.StaleAfter))
		out = append(out, m)
		return nil
	})
	return out, err
}

// StaleOnly filters ScanDir results.
func StaleOnly(all []Meta) []Meta {
	var out []Meta
	for _, m := range all {
		if m.Stale {
			out = append(out, m)
		}
	}
	return out
}

// Parse extracts freshness from optional YAML frontmatter.
func Parse(relPath string, raw []byte, mtime time.Time) (Meta, bool) {
	m := Meta{Path: filepath.ToSlash(relPath), LastValidated: mtime.UTC(), Source: "mtime"}
	fm, bodyOK := splitFrontmatter(raw)
	if !bodyOK || len(fm) == 0 {
		return m, false
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return m, false
	}
	has := false
	if v, ok := doc["stale-after"]; ok {
		if d, err := parseDuration(fmt.Sprint(v)); err == nil && d > 0 {
			m.StaleAfter = d
			has = true
		}
	}
	if !has {
		return m, false
	}
	if v, ok := doc["last-validated"]; ok {
		switch tv := v.(type) {
		case time.Time:
			m.LastValidated = tv.UTC()
			m.Source = "frontmatter"
		default:
			if t, err := parseDate(fmt.Sprint(v)); err == nil {
				m.LastValidated = t
				m.Source = "frontmatter"
			}
		}
	}
	if v, ok := doc["owner"]; ok {
		m.Owner = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := doc["confidence"]; ok {
		m.Confidence = strings.TrimSpace(fmt.Sprint(v))
	}
	return m, true
}

// StampValidated rewrites frontmatter last-validated to today (UTC date).
func StampValidated(raw []byte, when time.Time) ([]byte, error) {
	fm, rest, ok := splitFrontmatterParts(raw)
	day := when.UTC().Format("2006-01-02")
	if !ok {
		block := fmt.Sprintf("---\nlast-validated: %s\nstale-after: 30d\n---\n\n", day)
		return append([]byte(block), raw...), nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	doc["last-validated"] = day
	outFM, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(outFM)
	if !bytes.HasSuffix(outFM, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	b.Write(rest)
	return b.Bytes(), nil
}

func splitFrontmatter(raw []byte) (fm []byte, ok bool) {
	fm, _, ok = splitFrontmatterParts(raw)
	return fm, ok
}

func splitFrontmatterParts(raw []byte) (fm, rest []byte, ok bool) {
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return nil, raw, false
	}
	restS := s[3:]
	if strings.HasPrefix(restS, "\r\n") {
		restS = restS[2:]
	} else if strings.HasPrefix(restS, "\n") {
		restS = restS[1:]
	}
	idx := strings.Index(restS, "\n---")
	if idx < 0 {
		return nil, raw, false
	}
	fm = []byte(restS[:idx])
	after := restS[idx+4:] // after \n---
	after = strings.TrimPrefix(after, "\r")
	after = strings.TrimPrefix(after, "\n")
	return fm, []byte(after), true
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("bad date %q", s)
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "d") {
		var n int
		if _, err := fmt.Sscanf(s, "%dd", &n); err == nil {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return time.ParseDuration(s)
}
