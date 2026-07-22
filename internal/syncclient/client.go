package syncclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
)

// Client talks to a contextd server.
type Client struct {
	BaseURL string
	Token   string
	Space   string
	HTTP    *http.Client
}

// NewFromConfig builds a client from space config + token file.
func NewFromConfig(cfg *config.Config) (*Client, error) {
	if cfg.Mode != config.ModeClient {
		return nil, fmt.Errorf("not a client space (mode=%s)", cfg.Mode)
	}
	if cfg.Server.URL == "" || cfg.Server.Space == "" {
		return nil, fmt.Errorf("server.url and server.space required")
	}
	token, err := ReadToken(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		BaseURL: strings.TrimRight(cfg.Server.URL, "/"),
		Token:   token,
		Space:   cfg.Server.Space,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// ReadToken loads the bearer token.
func ReadToken(cfg *config.Config) (string, error) {
	path := cfg.Server.TokenFile
	if path == "" {
		path = filepath.Join(cfg.SpaceRoot, ".token")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token %s: %w", path, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// WriteToken stores the token with mode 0600.
func WriteToken(spaceRoot, token string) error {
	path := filepath.Join(spaceRoot, ".token")
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

func (c *Client) doBytes(ctx context.Context, method, path string, data []byte, ifMatch string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	if ifMatch != "" {
		req.Header.Set("If-Match", `"`+ifMatch+`"`)
	}
	return c.HTTP.Do(req)
}

// WhoAmI returns user/role.
func (c *Client) WhoAmI(ctx context.Context) (user, role string, err error) {
	res, err := c.do(ctx, http.MethodGet, "/api/v1/auth/whoami", nil)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", "", apiErr(res)
	}
	var out struct {
		User string `json:"user"`
		Role string `json:"role"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", "", err
	}
	return out.User, out.Role, nil
}

// Head returns space head.
func (c *Client) Head(ctx context.Context) (string, error) {
	res, err := c.do(ctx, http.MethodGet, "/api/v1/spaces/"+c.Space+"/head", nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", apiErr(res)
	}
	var out struct {
		Space string `json:"space"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Space, nil
}

// GetSpace returns space metadata including sync rules.
func (c *Client) GetSpace(ctx context.Context) (map[string]any, error) {
	res, err := c.do(ctx, http.MethodGet, "/api/v1/spaces/"+c.Space, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, apiErr(res)
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

type change struct {
	Path    string `json:"path"`
	Op      string `json:"op"`
	Version string `json:"version"`
}

// PullResult summarizes a pull.
type PullResult struct {
	Head     string
	Updated  int
	Skipped  int
	CheckOnly bool
}

// Pull syncs remote files into spaceRoot respecting selective sync.
func (c *Client) Pull(ctx context.Context, spaceRoot string, since string, sync spacesvc.SyncConfig, state *LocalState, checkOnly bool) (*PullResult, error) {
	res, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/spaces/%s/changes?since=%s", c.Space, since), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, apiErr(res)
	}
	var body struct {
		Head    string   `json:"head"`
		Changes []change `json:"changes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	result := &PullResult{Head: body.Head, CheckOnly: checkOnly}
	if checkOnly {
		for _, ch := range body.Changes {
			mode := ResolveMode(sync, ch.Path)
			if mode == "never" {
				result.Skipped++
				continue
			}
			if mode == "init-only" && state != nil && state.Seeded[ch.Path] {
				result.Skipped++
				continue
			}
			result.Updated++
		}
		return result, nil
	}
	for _, ch := range body.Changes {
		mode := ResolveMode(sync, ch.Path)
		if mode == "never" {
			result.Skipped++
			continue
		}
		if mode == "init-only" && state != nil && state.Seeded[ch.Path] {
			result.Skipped++
			continue
		}
		if ch.Op == "delete" {
			_ = os.Remove(filepath.Join(spaceRoot, filepath.FromSlash(ch.Path)))
			result.Updated++
			continue
		}
		data, err := c.fetchFile(ctx, ch.Path)
		if err != nil {
			return result, err
		}
		abs := filepath.Join(spaceRoot, filepath.FromSlash(ch.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return result, err
		}
		tmp := abs + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return result, err
		}
		if err := os.Rename(tmp, abs); err != nil {
			return result, err
		}
		if state != nil {
			if state.Seeded == nil {
				state.Seeded = map[string]bool{}
			}
			state.Seeded[ch.Path] = true
			state.Versions[ch.Path] = ch.Version
		}
		result.Updated++
	}
	logx.L().Info("pull complete", "head", body.Head, "updated", result.Updated, "skipped", result.Skipped)
	return result, nil
}

func (c *Client) fetchFile(ctx context.Context, path string) ([]byte, error) {
	res, err := c.do(ctx, http.MethodGet, "/api/v1/spaces/"+c.Space+"/files/"+path, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, apiErr(res)
	}
	return io.ReadAll(res.Body)
}

// PushResult summarizes a push.
type PushResult struct {
	Head    string
	Applied int
}

// Push uploads local files that differ from last known remote inventory.
// MVP: walk local tree and push all always/init-only paths as put ops.
func (c *Client) Push(ctx context.Context, spaceRoot string, expectedHead string, sync spacesvc.SyncConfig, checkOnly bool) (*PushResult, error) {
	var ops []spacesvc.PushOp
	err := filepath.WalkDir(spaceRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(spaceRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || d.IsDir() {
			if d.IsDir() && (rel == ".contextverse" || rel == ".git" || rel == ".sync" || strings.HasPrefix(rel, ".")) {
				if rel == "." {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "config.yaml" || rel == "meta.yaml" || rel == ".token" {
			return nil
		}
		mode := ResolveMode(sync, rel)
		if mode == "never" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		ops = append(ops, spacesvc.PushOp{
			Op:         "put",
			Path:       rel,
			ContentB64: base64.StdEncoding.EncodeToString(data),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if checkOnly {
		return &PushResult{Head: expectedHead, Applied: len(ops)}, nil
	}
	req := spacesvc.PushRequest{ExpectedHead: expectedHead, Ops: ops}
	res, err := c.do(ctx, http.MethodPost, "/api/v1/spaces/"+c.Space+"/push", req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusPreconditionFailed {
		return nil, fmt.Errorf("version_conflict: pull and retry")
	}
	if res.StatusCode != 200 {
		return nil, apiErr(res)
	}
	var out PushResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	logx.L().Info("push complete", "head", out.Head, "applied", out.Applied)
	return &out, nil
}

// LocalState tracks init-only seeding.
type LocalState struct {
	Seeded   map[string]bool   `json:"seeded"`
	Versions map[string]string `json:"versions"`
}

func statePath(spaceRoot string) string {
	return filepath.Join(spaceRoot, ".sync", "state.json")
}

// LoadState reads local sync state.
func LoadState(spaceRoot string) (*LocalState, error) {
	raw, err := os.ReadFile(statePath(spaceRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return &LocalState{Seeded: map[string]bool{}, Versions: map[string]string{}}, nil
		}
		return nil, err
	}
	var st LocalState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	if st.Seeded == nil {
		st.Seeded = map[string]bool{}
	}
	if st.Versions == nil {
		st.Versions = map[string]string{}
	}
	return &st, nil
}

// SaveState writes local sync state.
func SaveState(spaceRoot string, st *LocalState) error {
	if err := os.MkdirAll(filepath.Dir(statePath(spaceRoot)), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(spaceRoot), raw, 0o644)
}

// ResolveMode returns always|init-only|never for a path.
func ResolveMode(sync spacesvc.SyncConfig, path string) string {
	best := sync.Default
	if best == "" {
		best = "always"
	}
	bestLen := -1
	for _, r := range sync.Rules {
		prefix := strings.TrimSuffix(r.Path, "/")
		if r.Path == path || strings.HasPrefix(path, strings.TrimSuffix(r.Path, "*")) {
			if strings.HasSuffix(r.Path, "/") && strings.HasPrefix(path, r.Path) {
				if len(r.Path) > bestLen {
					best = r.Mode
					bestLen = len(r.Path)
				}
			} else if r.Path == path {
				if len(r.Path) > bestLen {
					best = r.Mode
					bestLen = len(r.Path)
				}
			} else if strings.HasSuffix(r.Path, "/") == false && strings.HasPrefix(path, prefix+"/") {
				if len(prefix) > bestLen {
					best = r.Mode
					bestLen = len(prefix)
				}
			}
		}
	}
	// simpler second pass
	for _, r := range sync.Rules {
		if strings.HasSuffix(r.Path, "/") {
			if strings.HasPrefix(path, r.Path) && len(r.Path) >= bestLen {
				best = r.Mode
				bestLen = len(r.Path)
			}
		} else if path == r.Path {
			best = r.Mode
			bestLen = len(r.Path)
		}
	}
	return best
}

// ParseSync extracts SyncConfig from GetSpace JSON.
func ParseSync(meta map[string]any) spacesvc.SyncConfig {
	cfg := spacesvc.DefaultSync()
	raw, ok := meta["sync"]
	if !ok {
		return cfg
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return cfg
	}
	var sc spacesvc.SyncConfig
	if err := json.Unmarshal(b, &sc); err != nil {
		return cfg
	}
	if sc.Default == "" {
		sc.Default = "always"
	}
	return sc
}

func apiErr(res *http.Response) error {
	raw, _ := io.ReadAll(res.Body)
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error.Code != "" {
		return fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
	}
	return fmt.Errorf("http %d: %s", res.StatusCode, string(raw))
}
