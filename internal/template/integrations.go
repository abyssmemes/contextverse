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
	// ClientIntegrationsPrefix is the archive path for session-start templates.
	ClientIntegrationsPrefix = "client-integrations/"
	// ClientIntegrationsCacheDirName under the user cache directory.
	ClientIntegrationsCacheDirName = "contextverse/client-integrations"
)

// ListClientIntegrations lists remote client-integration IDs from the catalog repo.
func ListClientIntegrations(repo, ref string, client *http.Client) ([]Entry, error) {
	return ListContents(repo, ref, "client-integrations", client)
}

// ListContents lists directories under path in the GitHub repo (contents API).
func ListContents(repo, ref, path string, client *http.Client) ([]Entry, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	if ref == "" {
		ref = DefaultRef
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s", repo, path, ref)
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
		return nil, fmt.Errorf("list %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("list %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var items []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	var out []Entry
	for _, it := range items {
		if it.Type == "dir" {
			out = append(out, Entry{Name: it.Name})
		}
	}
	return out, nil
}

// SyncClientIntegrations downloads all client-integrations/* into the cache and returns the cache dir.
func SyncClientIntegrations(repo, ref string, refresh bool, client *http.Client) (string, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	if ref == "" {
		ref = DefaultRef
	}
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(base, ClientIntegrationsCacheDirName, repo, ref)
	marker := filepath.Join(dest, ".synced")
	if !refresh {
		if st, err := os.Stat(marker); err == nil && time.Since(st.ModTime()) < 24*time.Hour {
			return dest, nil
		}
		// still use cache if populated even when marker stale — refresh only when asked or empty
		if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 && !refresh {
			if _, err := os.Stat(marker); err == nil {
				return dest, nil
			}
		}
	}

	logx.L().Info("fetching client-integrations from catalog", "repo", repo, "ref", ref)
	if err := fetchTreeIntoCache(client, repo, ref, ClientIntegrationsPrefix, dest); err != nil {
		return "", err
	}
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return dest, nil
	}
	return dest, nil
}

func fetchTreeIntoCache(client *http.Client, repo, ref, prefix, dest string) error {
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
		return fmt.Errorf("download catalog archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("download catalog archive: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	staging := dest + ".staging"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	if err := extractTreeFromTarGz(resp.Body, prefix, staging); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	_ = os.RemoveAll(dest)
	if err := os.Rename(staging, dest); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	logx.L().Info("client-integrations cached", "path", dest)
	return nil
}

// extractTreeFromTarGz extracts archive members under prefix into dest, keeping
// the first path segment after prefix as a subdirectory (e.g. client-integrations/foo/…).
func extractTreeFromTarGz(r io.Reader, prefix, dest string) error {
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
			return err
		}
		name := hdr.Name
		idx := strings.Index(name, prefix)
		if idx < 0 {
			continue
		}
		rel := name[idx+len(prefix):]
		if rel == "" {
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
		return fmt.Errorf("prefix %q not found in archive", prefix)
	}
	return nil
}
