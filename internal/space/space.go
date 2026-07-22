package space

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/template"
)

//go:embed embed/solo-default/**
var embeddedTemplates embed.FS

const embeddedSoloDefault = "embed/solo-default"

// IdentityFields fill identity/me.md during init.
type IdentityFields struct {
	Name     string
	Role     string
	Language string
	Tools    string
}

// CreateOptions controls space creation.
type CreateOptions struct {
	SpaceRoot    string
	TemplateName string // catalog name (default solo-default)
	TemplatePath string // if set, copy from this directory instead of catalog/embed
	Identity     IdentityFields
	Force        bool
	RefreshTemplate bool // re-fetch remote template, ignore cache
	SkipIdentity    bool // used by space seed — don't rewrite identity/me.md
}

// Create seeds a new context space from a template and optionally writes identity.
func Create(opts CreateOptions) error {
	if opts.SpaceRoot == "" {
		return fmt.Errorf("space root is required")
	}
	log := logx.L()

	if !opts.Force {
		if _, err := os.Stat(filepath.Join(opts.SpaceRoot, "context-entry.md")); err == nil {
			return fmt.Errorf("space already exists at %s (pass --force to overwrite template files)", opts.SpaceRoot)
		}
	}

	if err := os.MkdirAll(opts.SpaceRoot, 0o755); err != nil {
		return fmt.Errorf("create space root: %w", err)
	}

	if err := seedFromTemplate(opts); err != nil {
		return err
	}

	// template.yaml is meta for the catalog — not part of a live space
	_ = os.Remove(filepath.Join(opts.SpaceRoot, "template.yaml"))

	if !opts.SkipIdentity {
		if err := writeIdentity(opts.SpaceRoot, opts.Identity); err != nil {
			return err
		}
	}

	log.Info("space created", "root", opts.SpaceRoot)
	return nil
}

func seedFromTemplate(opts CreateOptions) error {
	log := logx.L()
	name := opts.TemplateName
	if name == "" {
		name = "solo-default"
	}

	var preservedIdentity []byte
	identityPath := filepath.Join(opts.SpaceRoot, "identity", "me.md")
	if opts.SkipIdentity {
		if data, err := os.ReadFile(identityPath); err == nil {
			preservedIdentity = data
			log.Info("preserving existing identity during seed", "path", identityPath)
		}
	}

	resolved, err := template.Resolve(template.ResolveOptions{
		Name:    name,
		Path:    opts.TemplatePath,
		Refresh: opts.RefreshTemplate,
	})
	if err == nil {
		log.Info("seeding space from template", "name", resolved.Name, "source", string(resolved.Source), "path", resolved.DiskPath)
		if err := copyDir(resolved.DiskPath, opts.SpaceRoot); err != nil {
			return err
		}
	} else {
		// Offline / network failure: only solo-default has an embedded copy.
		log.Warn("catalog template unavailable; trying embedded fallback", "template", name, "err", err)
		if name != "solo-default" && opts.TemplatePath == "" {
			return fmt.Errorf("template %q unavailable: %w (and no embedded copy; use --template-path or fix network)", name, err)
		}
		log.Info("seeding space from embedded template", "template", "solo-default", "root", opts.SpaceRoot)
		if err := copyEmbedded(embeddedSoloDefault, opts.SpaceRoot); err != nil {
			return err
		}
	}

	if len(preservedIdentity) > 0 {
		if err := os.MkdirAll(filepath.Dir(identityPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(identityPath, preservedIdentity, 0o644); err != nil {
			return fmt.Errorf("restore identity: %w", err)
		}
	}
	return nil
}

func copyEmbedded(srcRoot, dstRoot string) error {
	return fs.WalkDir(embeddedTemplates, srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := embeddedTemplates.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func writeIdentity(spaceRoot string, id IdentityFields) error {
	path := filepath.Join(spaceRoot, "identity", "me.md")
	today := time.Now().UTC().Format("2006-01-02")
	name := id.Name
	if name == "" {
		name = "…"
	}
	role := id.Role
	if role == "" {
		role = "…"
	}
	lang := id.Language
	if lang == "" {
		lang = "English"
	}
	tools := id.Tools
	if tools == "" {
		tools = "…"
	}

	content := fmt.Sprintf(`---
freshness: current
last-validated: %s
stale-after: 90d
confidence: medium
importance: high
---

# Me

## Who I am

- **Name:** %s
- **Role:** %s

## Tools

%s

## Preferences

- **Preferred language:** %s
`, today, name, role, tools, lang)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create identity dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write identity: %w", err)
	}
	logx.L().Info("wrote identity", "path", path)
	return nil
}

// RequiredFiles are the minimum files a healthy space should have.
var RequiredFiles = []string{
	"context-entry.md",
	"space-index.md",
	"decisions.md",
	"identity/me.md",
	"team/principles.md",
	"team/skill-map.md",
	"team/space-map.md",
}

// Status describes the on-disk space for `contextd status`.
type Status struct {
	SpaceRoot     string
	Exists        bool
	Missing       []string
	Projects      []string
	IdentityName  string
	IndexProjects int
}

// Inspect returns space health without mutating anything.
func Inspect(spaceRoot string) (*Status, error) {
	st := &Status{SpaceRoot: spaceRoot}
	if _, err := os.Stat(spaceRoot); err != nil {
		if os.IsNotExist(err) {
			st.Exists = false
			return st, nil
		}
		return nil, err
	}
	st.Exists = true

	for _, rel := range RequiredFiles {
		p := filepath.Join(spaceRoot, rel)
		if _, err := os.Stat(p); err != nil {
			st.Missing = append(st.Missing, rel)
		}
	}

	projectsDir := filepath.Join(spaceRoot, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				st.Projects = append(st.Projects, e.Name())
			}
		}
	}

	mePath := filepath.Join(spaceRoot, "identity", "me.md")
	if data, err := os.ReadFile(mePath); err == nil {
		st.IdentityName = extractName(string(data))
	}

	return st, nil
}

func extractName(md string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- **Name:**") {
			return strings.TrimSpace(strings.TrimPrefix(line, "- **Name:**"))
		}
	}
	return ""
}

// UpdateIndex regenerates space-index.md from the projects/ directory and known key files.
func UpdateIndex(spaceRoot string) error {
	logx.L().Info("updating space index", "root", spaceRoot)
	projectsDir := filepath.Join(spaceRoot, "projects")
	var projectRows []string
	entries, err := os.ReadDir(projectsDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read projects: %w", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	projectCount := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		projectCount++
		projectRows = append(projectRows, fmt.Sprintf("| %s | active | — | — | %s |", e.Name(), today))
	}
	if len(projectRows) == 0 {
		projectRows = append(projectRows, "| — | — | — | — | — |")
	}

	content := fmt.Sprintf(`# Space Index
Last validated: %s

## Projects
| Project | Status | Owner | Dependencies | Last validated |
|---------|--------|-------|--------------|----------------|
%s

## Key Files
- context-entry.md — routing for any AI
- identity/me.md — who you are
- team/principles.md — how we work
- team/skill-map.md — capabilities
- team/space-map.md — navigation
- decisions.md — decision log

Update this index when you add or remove meaningful context, or run: contextd index update
`, today, strings.Join(projectRows, "\n"))

	path := filepath.Join(spaceRoot, "space-index.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write space-index: %w", err)
	}
	logx.L().Info("space index written", "path", path, "projects", projectCount)
	return nil
}
