package template

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abyssmemes/contextverse/internal/logx"
)

const (
	// DefaultRepo is the public templates catalog.
	DefaultRepo = "abyssmemes/contextverse-templates"
	// DefaultRef is the git ref to fetch from.
	DefaultRef = "main"
	// CacheDirName under the user cache directory.
	CacheDirName = "contextverse/templates"
)

// Source describes where a resolved template came from.
type Source string

const (
	SourceLocalPath Source = "local-path"
	SourceCache     Source = "cache"
	SourceRemote    Source = "remote"
	SourceEmbedded  Source = "embedded" // caller handles embed; we only signal fallback
)

// ResolveOptions control template lookup.
type ResolveOptions struct {
	Name     string // catalog name, e.g. solo-default
	Path     string // explicit local directory (wins)
	Repo     string // owner/name, default DefaultRepo
	Ref      string // git ref, default DefaultRef
	Refresh  bool   // ignore cache, re-fetch
	HTTPClient *http.Client
}

// Resolved is a template ready to copy from DiskPath.
type Resolved struct {
	Name     string
	DiskPath string
	Source   Source
	Repo     string
	Ref      string
}

// Resolve finds a template directory.
// Order: Path → cache (unless Refresh) → remote catalog → error (caller may fall back to embed).
func Resolve(opts ResolveOptions) (*Resolved, error) {
	if opts.Path != "" {
		info, err := os.Stat(opts.Path)
		if err != nil {
			return nil, fmt.Errorf("template path: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("template path is not a directory: %s", opts.Path)
		}
		return &Resolved{Name: filepath.Base(opts.Path), DiskPath: opts.Path, Source: SourceLocalPath}, nil
	}

	name := opts.Name
	if name == "" {
		name = "solo-default"
	}
	repo := opts.Repo
	if repo == "" {
		repo = DefaultRepo
	}
	ref := opts.Ref
	if ref == "" {
		ref = DefaultRef
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	cacheRoot, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	cached := filepath.Join(cacheRoot, repo, ref, name)
	marker := filepath.Join(cached, "context-entry.md")

	if !opts.Refresh {
		if _, err := os.Stat(marker); err == nil {
			logx.L().Info("using cached template", "name", name, "path", cached)
			return &Resolved{Name: name, DiskPath: cached, Source: SourceCache, Repo: repo, Ref: ref}, nil
		}
	}

	logx.L().Info("fetching template from catalog", "repo", repo, "ref", ref, "name", name)
	if err := fetchIntoCache(client, repo, ref, name, cached); err != nil {
		return nil, err
	}
	if _, err := os.Stat(marker); err != nil {
		return nil, fmt.Errorf("template %q fetched but missing context-entry.md (not in catalog?)", name)
	}
	return &Resolved{Name: name, DiskPath: cached, Source: SourceRemote, Repo: repo, Ref: ref}, nil
}

// Entry is a catalog listing row.
type Entry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

// List returns template names from the remote catalog (GitHub contents API).
func List(repo, ref string, client *http.Client) ([]Entry, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	if ref == "" {
		ref = DefaultRef
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/templates?ref=%s", repo, ref)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "contextd")
	if tok := githubToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("list templates: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var items []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode template list: %w", err)
	}

	var out []Entry
	for _, it := range items {
		if it.Type != "dir" {
			continue
		}
		out = append(out, Entry{Name: it.Name})
	}
	return out, nil
}

func cacheRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("user cache dir: %w", err)
	}
	root := filepath.Join(base, CacheDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

func githubToken() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

func fetchIntoCache(client *http.Client, repo, ref, name, dest string) error {
	// codeload tarball of the whole repo; extract templates/<name>/
	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/%s", repo, ref)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "contextd")
	if tok := githubToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download templates archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("download templates archive: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	tmpParent := filepath.Dir(dest)
	if err := os.MkdirAll(tmpParent, 0o755); err != nil {
		return err
	}
	staging := dest + ".staging"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}

	prefix := "templates/" + name + "/"
	if err := extractTemplateFromTarGz(resp.Body, prefix, staging); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}

	_ = os.RemoveAll(dest)
	if err := os.Rename(staging, dest); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("install template cache: %w", err)
	}
	logx.L().Info("template cached", "name", name, "path", dest)
	return nil
}

// extractTemplateFromTarGz copies members under prefix into dest, stripping the
// leading "<repo>-<ref>/" and "templates/<name>/" segments from the archive.
func extractTemplateFromTarGz(r io.Reader, prefix, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		// archive paths look like: contextverse-templates-main/templates/solo-default/...
		name := hdr.Name
		idx := strings.Index(name, prefix)
		if idx < 0 {
			continue
		}
		rel := name[idx+len(prefix):]
		if rel == "" {
			if hdr.Typeflag == tar.TypeDir {
				found = true
			}
			continue
		}
		found = true
		target := filepath.Join(dest, rel)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o755|0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("template prefix %q not found in archive", prefix)
	}
	return nil
}
