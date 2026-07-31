package syncclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/orkcom-tech/contextverse/internal/config"
	"github.com/orkcom-tech/contextverse/internal/logx"
	"github.com/orkcom-tech/contextverse/internal/spacesvc"
	"github.com/orkcom-tech/contextverse/internal/storage"
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

// SpaceInfo is one space the caller is allowed to see.
type SpaceInfo struct {
	Name string `json:"name"`
	Head string `json:"head,omitempty"`
}

// ListSpaces returns the spaces this token can see. The server has always
// exposed this; the client never asked, so joining a team meant typing a space
// name you had to be told out of band.
//
// It then went on never asking successfully. The server answers
// {"spaces":[...]} and this decoded into a bare []SpaceInfo, so every call
// failed with "cannot unmarshal object into []SpaceInfo" — and the wizard's
// fallback swallowed it and told the person "the server did not return a
// listing for this token", blaming the server for a bug on this side. The space
// picker has never once run.
func (c *Client) ListSpaces(ctx context.Context) ([]SpaceInfo, error) {
	res, err := c.do(ctx, http.MethodGet, "/api/v1/spaces", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, apiErr(res)
	}
	var out struct {
		Spaces []SpaceInfo `json:"spaces"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("list spaces: %w", err)
	}
	return out.Spaces, nil
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
	Head      string
	Updated   int
	Skipped   int
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
		// The server names these paths. A hostile or compromised server must not
		// be able to write outside the space it is syncing.
		abs, err := storage.ResolveUnder(spaceRoot, ch.Path)
		if err != nil {
			return result, fmt.Errorf("refusing server path %q: %w", ch.Path, err)
		}
		if ch.Op == "delete" {
			_ = os.Remove(abs)
			result.Updated++
			continue
		}
		data, err := c.fetchFile(ctx, ch.Path)
		if err != nil {
			return result, err
		}
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

// Push sends what has changed on this machine since the last successful push.
//
// It used to walk the tree and put every file, every time, base64-encoded into
// one JSON body — so a space of a thousand documents re-uploaded a thousand
// documents to publish an edit to one. And it never sent a delete, so a file
// removed locally came back on the next pull, which is the kind of thing that
// makes people stop trusting a sync tool.
//
// state carries what this machine last sent. Passing nil falls back to sending
// everything, which is right for a caller that has no record to compare against.
func (c *Client) Push(ctx context.Context, spaceRoot string, expectedHead string, sync spacesvc.SyncConfig, state *LocalState, checkOnly bool) (*PushResult, error) {
	ops, sent, err := collectPushOps(spaceRoot, sync, state)
	if err != nil {
		return nil, err
	}
	if checkOnly {
		return &PushResult{Head: expectedHead, Applied: len(ops)}, nil
	}
	if len(ops) == 0 {
		// Nothing to say. Sending an empty batch would move the head for no
		// reason and make every other client re-check a space that did not
		// change.
		logx.L().Info("push: nothing changed since the last one")
		return &PushResult{Head: expectedHead, Applied: 0}, nil
	}
	req := spacesvc.PushRequest{ExpectedHead: expectedHead, Ops: ops}
	res, err := c.do(ctx, http.MethodPost, "/api/v1/spaces/"+c.Space+"/push", req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusPreconditionFailed {
		return nil, &APIError{
			Status:  http.StatusPreconditionFailed,
			Code:    "version_conflict",
			Message: "the server moved ahead of your last sync — pull and retry",
		}
	}
	if res.StatusCode != 200 {
		return nil, apiErr(res)
	}
	var out PushResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	// Recorded only after the server accepted the batch. Writing it earlier
	// would make a failed push look like a delivered one, and the next push
	// would skip the very files that never arrived.
	if state != nil {
		state.Sent = sent
	}
	logx.L().Info("push complete", "head", out.Head, "applied", out.Applied)
	return &out, nil
}

// LocalState is what this machine remembers about the last sync.
type LocalState struct {
	Seeded   map[string]bool   `json:"seeded"`
	Versions map[string]string `json:"versions"`
	// Sent records the content of each path as this machine last put it on the
	// server, so a push can carry the difference instead of the whole space.
	//
	// Hashes rather than the server's version markers, because the question a
	// push asks is "did I change this", which is about local content. It is also
	// what makes a deletion visible: a path in here with no file beside it was
	// pushed once and has since been removed.
	Sent map[string]string `json:"sent,omitempty"`
}

func statePath(spaceRoot string) string {
	return filepath.Join(spaceRoot, ".sync", "state.json")
}

// LoadState reads local sync state.
func LoadState(spaceRoot string) (*LocalState, error) {
	raw, err := os.ReadFile(statePath(spaceRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return &LocalState{
				Seeded:   map[string]bool{},
				Versions: map[string]string{},
				Sent:     map[string]string{},
			}, nil
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
	if st.Sent == nil {
		st.Sent = map[string]string{}
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

// ResolveMode decides whether a path syncs, and it decides what leaves this
// machine — so it is worth being able to read.
//
// Longest match wins, and an exact path beats a prefix of the same length. That
// is the rule people already assume from every ignore-file they have used, and
// it was not what the code did: there were two passes, the first with conditions
// that contradicted each other and could not be reasoned about, the second
// overwriting the first with a >= comparison that made the answer depend on the
// order the rules happened to be listed in. Two configurations with the same
// rules in a different order gave different answers about whether somebody's
// identity file was published to their team.
//
// A rule ending in "/" matches a subtree. Anything else matches that exact path.
func ResolveMode(sync spacesvc.SyncConfig, path string) string {
	best := sync.Default
	if best == "" {
		best = "always"
	}
	bestLen := -1
	bestExact := false

	for _, r := range sync.Rules {
		if r.Path == "" || r.Mode == "" {
			continue
		}
		exact := !strings.HasSuffix(r.Path, "/")
		switch {
		case exact && path == r.Path:
		case !exact && strings.HasPrefix(path, r.Path):
		default:
			continue
		}
		// Ties go to the more specific rule rather than to whichever came last:
		// "identity/me.md" beats "identity/" even though the strings are close
		// in length, and an equal-length prefix never displaces an exact match.
		if len(r.Path) > bestLen || (len(r.Path) == bestLen && exact && !bestExact) {
			best, bestLen, bestExact = r.Mode, len(r.Path), exact
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

// APIError carries the HTTP status alongside the server's error envelope.
// Callers map the status to an exit code (auth vs. conflict vs. everything
// else); without it they would have to pattern-match on message text.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("http %d: %s", e.Status, e.Message)
}

// Unauthorized reports a credential or permission failure.
func (e *APIError) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// Conflict reports a compare-and-swap failure (the 412 the API returns when the
// caller's version marker is stale).
func (e *APIError) Conflict() bool {
	return e.Status == http.StatusPreconditionFailed || e.Status == http.StatusConflict
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
		return &APIError{Status: res.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
	}
	return &APIError{Status: res.StatusCode, Message: string(raw)}
}

// collectPushOps gathers what this client should send upward, and what the
// record of "sent" will be once the server accepts it.
//
// Split out of Push so the filter can be tested without a server: what does and
// does not leave the machine is a question worth being able to ask directly.
func collectPushOps(spaceRoot string, sync spacesvc.SyncConfig, state *LocalState) ([]spacesvc.PushOp, map[string]string, error) {
	previous := map[string]string{}
	if state != nil && state.Sent != nil {
		previous = state.Sent
	}

	var ops []spacesvc.PushOp
	sent := make(map[string]string, len(previous))

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
		switch ResolveMode(sync, rel) {
		case "never":
			return nil
		case "init-only":
			// Seeded once from the server, then the local copy is the person's
			// own. Pull already refuses to overwrite it; pushing it anyway sent
			// a real name, role and preferences into the space the whole team
			// pulls from, and the asymmetry is what kept it invisible — nothing
			// on your own machine ever changed.
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := contentHash(data)
		sent[rel] = sum
		if prev, ok := previous[rel]; ok && prev == sum {
			return nil // unchanged since the last accepted push
		}
		ops = append(ops, spacesvc.PushOp{
			Op:         "put",
			Path:       rel,
			ContentB64: base64.StdEncoding.EncodeToString(data),
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// A path we sent before and cannot find now was deleted here. Without this
	// the server kept its copy and the next pull put the file back, which reads
	// as the tool undoing your work.
	for rel := range previous {
		if _, stillHere := sent[rel]; stillHere {
			continue
		}
		if ResolveMode(sync, rel) == "never" {
			continue
		}
		ops = append(ops, spacesvc.PushOp{Op: "delete", Path: rel})
	}

	return ops, sent, nil
}

// contentHash identifies a file's contents for the "did I change this" question.
func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
