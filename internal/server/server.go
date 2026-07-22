package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abyssmemes/contextverse/internal/audit"
	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/hooks"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
	"github.com/abyssmemes/contextverse/internal/storage"
	"github.com/abyssmemes/contextverse/internal/version"
	"github.com/abyssmemes/contextverse/internal/webhooks"
)

// Server is the HTTP data-plane + admin UI process.
type Server struct {
	Cfg      *config.ServerConfig
	Auth     *auth.Store
	Authz    *authz.Engine
	Spaces   *spacesvc.Service
	Audit    *audit.Logger
	Hooks    *webhooks.Store
	Dispatch *webhooks.Dispatcher
	http     *http.Server

	mu           sync.Mutex
	NeedsSetup   bool
	setupDataDir string
	setupAddr    string
	setupPort    int
}

// New constructs a Server from an opened data dir.
func New(cfg *config.ServerConfig, authStore *auth.Store) *Server {
	eng, err := authz.Open(authStore.PoliciesDir())
	if err != nil {
		logx.L().Error("open authz engine", "err", err)
	}
	al, err := audit.Open(cfg.DataDir)
	if err != nil {
		logx.L().Error("open audit log", "err", err)
	}
	wh, err := webhooks.Open(cfg.DataDir)
	if err != nil {
		logx.L().Error("open webhooks", "err", err)
	}
	hookCfg, err := hooks.Load(cfg.DataDir)
	if err != nil {
		logx.L().Warn("load hooks.yaml", "err", err)
	}
	return &Server{
		Cfg:      cfg,
		Auth:     authStore,
		Authz:    eng,
		Spaces:   &spacesvc.Service{DataDir: cfg.DataDir, Backend: cfg.Backend, Hooks: hookCfg},
		Audit:    al,
		Hooks:    wh,
		Dispatch: webhooks.NewDispatcher(wh),
	}
}

// NewSetup creates a first-run install wizard server (no config yet).
func NewSetup(dataDir, address string, port int) *Server {
	if address == "" {
		address = config.DefaultListenAddr
	}
	if port == 0 {
		port = config.DefaultListenPort
	}
	if dataDir == "" {
		dataDir, _ = config.DefaultServerDataDir()
	}
	return &Server{
		NeedsSetup:   true,
		setupDataDir: dataDir,
		setupAddr:    address,
		setupPort:    port,
		Cfg: &config.ServerConfig{
			Listen:  config.ListenConfig{Address: address, Port: port},
			DataDir: dataDir,
		},
	}
}

// Handler returns the root mux (rebuilt each call so setup→running works).
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.registerUI(mux)

	if !s.NeedsSetup {
		mux.HandleFunc("POST /api/v1/auth/userpass/login", s.handleUserpassLogin)
		mux.Handle("GET /api/v1/auth/whoami", s.auth(s.handleWhoAmI))
		mux.Handle("DELETE /api/v1/auth/token", s.auth(s.handleRevokeToken))
		mux.Handle("GET /api/v1/spaces", s.auth(s.handleListSpaces))
		mux.Handle("POST /api/v1/spaces", s.auth(s.handleCreateSpace))
		mux.Handle("GET /api/v1/spaces/{space}", s.auth(s.handleGetSpace))
		mux.Handle("DELETE /api/v1/spaces/{space}", s.auth(s.handleDeleteSpace))
		mux.Handle("GET /api/v1/spaces/{space}/tree", s.auth(s.handleTree))
		mux.Handle("GET /api/v1/spaces/{space}/files/{path...}", s.auth(s.handleGetFile))
		mux.Handle("PUT /api/v1/spaces/{space}/files/{path...}", s.auth(s.handlePutFile))
		mux.Handle("DELETE /api/v1/spaces/{space}/files/{path...}", s.auth(s.handleDeleteFile))
		mux.Handle("GET /api/v1/spaces/{space}/versions/{path...}", s.auth(s.handleListFileVersions))
		mux.Handle("DELETE /api/v1/spaces/{space}/versions/{path...}", s.auth(s.handleDestroyFileVersion))
		mux.Handle("POST /api/v1/spaces/{space}/undelete/{path...}", s.auth(s.handleUndeleteFile))
		mux.Handle("GET /api/v1/spaces/{space}/head", s.auth(s.handleHead))
		mux.Handle("GET /api/v1/spaces/{space}/changes", s.auth(s.handleChanges))
		mux.Handle("POST /api/v1/spaces/{space}/push", s.auth(s.handlePush))
		mux.Handle("GET /api/v1/spaces/{space}/snapshots", s.auth(s.handleListSnapshots))
		mux.Handle("POST /api/v1/spaces/{space}/snapshots", s.auth(s.handleTakeSnapshot))
		mux.Handle("GET /api/v1/audit", s.auth(s.handleAuditList))
		mux.Handle("GET /api/v1/audit/export", s.auth(s.handleAuditExport))
		mux.Handle("GET /api/v1/audit/stats", s.auth(s.handleAuditStats))
		mux.Handle("GET /api/v1/webhooks", s.auth(s.handleWebhooksList))
		mux.Handle("POST /api/v1/webhooks", s.auth(s.handleWebhooksCreate))
		mux.Handle("GET /api/v1/webhooks/{id}", s.auth(s.handleWebhooksGet))
		mux.Handle("DELETE /api/v1/webhooks/{id}", s.auth(s.handleWebhooksDelete))
		mux.Handle("POST /api/v1/webhooks/{id}/test", s.auth(s.handleWebhooksTest))
		mux.Handle("GET /api/v1/webhooks/dead-letter", s.auth(s.handleWebhooksDeadLetter))
	}

	return s.withRequestID(mux)
}

// ListenAndServe starts the HTTP server (blocking).
func (s *Server) ListenAndServe() error {
	s.http = &http.Server{
		Addr: s.Cfg.Addr(),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.Handler().ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", s.Cfg.Addr())
	if err != nil {
		return fmt.Errorf("listen %s: %w\nHint: another contextd may still be running. Try: lsof -iTCP:%d -sTCP:LISTEN then contextd server stop (or kill <pid>)",
			s.Cfg.Addr(), err, s.Cfg.Listen.Port)
	}
	logx.L().Info("server listening", "addr", s.Cfg.Addr(), "data_dir", s.Cfg.DataDir, "setup", s.NeedsSetup, "tls", s.Cfg.TLS.Enabled)
	if !s.Cfg.TLS.Enabled && !isLoopbackListen(s.Cfg.Listen.Address) {
		logx.L().Warn("TLS disabled while listening on a non-loopback address — traffic is plaintext; enable tls in config or bind 127.0.0.1",
			"address", s.Cfg.Listen.Address)
	}
	if s.Cfg.TLS.Enabled {
		if s.Cfg.TLS.CertFile == "" || s.Cfg.TLS.KeyFile == "" {
			return fmt.Errorf("tls.enabled requires tls.cert_file and tls.key_file")
		}
		return s.http.ServeTLS(ln, s.Cfg.TLS.CertFile, s.Cfg.TLS.KeyFile)
	}
	return s.http.Serve(ln)
}

func isLoopbackListen(addr string) bool {
	a := strings.TrimSpace(strings.ToLower(addr))
	return a == "127.0.0.1" || a == "::1" || a == "localhost" || a == ""
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

type ctxKey int

const principalKey ctxKey = 1

func principalFrom(ctx context.Context) *auth.Principal {
	p, _ := ctx.Value(principalKey).(*auth.Principal)
	return p
}

func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			writeErr(w, r, http.StatusUnauthorized, "unauthenticated", "missing bearer token", nil)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		p, err := s.Auth.Authenticate(token)
		if err != nil {
			writeErr(w, r, http.StatusUnauthorized, "unauthenticated", "invalid token", nil)
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, p)
		next(w, r.WithContext(ctx))
	})
}

func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": version.Version,
	})
}

func (s *Server) handleUserpassLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "invalid json", nil)
		return
	}
	tok, rec, err := s.Auth.LoginUserpass(body.Username, body.Password)
	if err != nil {
		writeErr(w, r, http.StatusUnauthorized, "unauthenticated", err.Error(), nil)
		return
	}
	logx.L().Info("userpass login", "user", body.Username, "token_id", rec.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":    tok,
		"token_id": rec.ID,
		"user":     rec.User,
		"policies": rec.EffectivePolicies(),
	})
}

func (s *Server) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	pols := p.Policies
	if len(pols) == 0 && p.Role != "" {
		pols = []string{string(p.Role)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":     p.User,
		"role":     p.Role,
		"policies": pols,
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if err := s.Auth.RevokePrincipalToken(p); err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSpaces(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "spaces/", authz.CapList) {
		return
	}
	names, err := s.Spaces.List()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	type item struct {
		Name string `json:"name"`
		Head string `json:"head,omitempty"`
	}
	out := make([]item, 0, len(names))
	for _, n := range names {
		h, _ := s.Spaces.Head(r.Context(), n)
		out = append(out, item{Name: n, Head: string(h)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"spaces": out})
}

func (s *Server) handleCreateSpace(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "spaces/", authz.CapCreate) {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "invalid json", nil)
		return
	}
	meta, err := s.Spaces.Create(r.Context(), body.Name, body.Template, false)
	if err != nil {
		writeErr(w, r, http.StatusConflict, "conflict", err.Error(), nil)
		return
	}
	s.auditEmit(r, "space.create", body.Name, body.Template, nil)
	writeJSON(w, http.StatusCreated, meta)
}

func (s *Server) handleGetSpace(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name, authz.CapRead) {
		return
	}
	meta, err := s.Spaces.LoadMeta(name)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", "space not found", nil)
		return
	}
	head, _ := s.Spaces.Head(r.Context(), name)
	writeJSON(w, http.StatusOK, map[string]any{
		"name":     meta.Name,
		"template": meta.Template,
		"created":  meta.CreatedAt,
		"head":     string(head),
		"sync":     meta.Sync,
	})
}

func (s *Server) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name, authz.CapDelete) {
		return
	}
	if err := s.Spaces.Delete(name); err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	s.auditEmit(r, "space.delete", name, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name+"/files", authz.CapList) {
		return
	}
	entries, err := s.Spaces.Tree(r.Context(), name)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	path := r.PathValue("path")
	if !s.requireCap(w, r, fmt.Sprintf("spaces/%s/files/%s", name, path), authz.CapRead) {
		return
	}
	if v := strings.TrimSpace(r.URL.Query().Get("version")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, r, http.StatusBadRequest, "invalid_request", "version must be a positive integer", nil)
			return
		}
		data, info, err := s.Spaces.GetFileVersion(r.Context(), name, path, n)
		if errors.Is(err, storage.ErrNotFound) {
			writeErr(w, r, http.StatusNotFound, "not_found", "version not found", nil)
			return
		}
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
			return
		}
		w.Header().Set("ETag", etag(storage.FormatFileVersion(info.Version)))
		w.Header().Set("X-ContextVerse-File-Version", strconv.Itoa(info.Version))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	data, ver, err := s.Spaces.GetFile(r.Context(), name, path)
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "not_found", "file not found", nil)
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	w.Header().Set("ETag", etag(ver))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleListFileVersions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	path := r.PathValue("path")
	if !s.requireCap(w, r, fmt.Sprintf("spaces/%s/files/%s", name, path), authz.CapRead) {
		return
	}
	meta, versions, err := s.Spaces.ListFileVersions(r.Context(), name, path)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     path,
		"current":  meta.Current,
		"versions": versions,
	})
}

func (s *Server) handleUndeleteFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	path := r.PathValue("path")
	if !s.requireFileWrite(w, r, name, path) {
		return
	}
	ver, err := s.Spaces.UndeleteFile(r.Context(), name, path)
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "not_found", "nothing to undelete", nil)
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	_, _ = s.bumpHead(r.Context(), name)
	w.Header().Set("ETag", etag(ver))
	s.auditEmit(r, "file.undelete", name, path, nil)
	writeJSON(w, http.StatusOK, map[string]any{"version": string(ver)})
}

func (s *Server) handleDestroyFileVersion(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	path := r.PathValue("path")
	if !s.requireCap(w, r, fmt.Sprintf("spaces/%s/files/%s", name, path), authz.CapDelete) {
		return
	}
	v := strings.TrimSpace(r.URL.Query().Get("version"))
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "query version=N required", nil)
		return
	}
	err = s.Spaces.DestroyFileVersion(r.Context(), name, path, n)
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "not_found", "version not found", nil)
		return
	}
	if errors.Is(err, storage.ErrInvalidArgument) {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	s.auditEmit(r, "file.version.destroy", name, path+"?version="+v, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePutFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	path := r.PathValue("path")
	if !s.requireFileWrite(w, r, name, path) {
		return
	}
	expected, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "read body", nil)
		return
	}
	ver, err := s.Spaces.PutFile(r.Context(), name, path, data, expected)
	if errors.Is(err, storage.ErrConflict) {
		writeErr(w, r, http.StatusPreconditionFailed, "version_conflict", err.Error(), nil)
		return
	}
	var blocked *hooks.BlockedError
	if errors.As(err, &blocked) {
		s.auditWrite(r, "secret.blocked", name, path, audit.ResultDenied, blocked.Error(), nil)
		if s.Dispatch != nil {
			s.Dispatch.Emit(webhooks.Event{
				Type:  "secret.blocked",
				Space: name,
				Scope: path,
				Actor: actorFrom(r, principalFrom(r.Context())).Username,
				Data: map[string]any{
					"path":     path,
					"rule":     blocked.Findings[0].Rule,
					"findings": blocked.Findings,
				},
			})
		}
		writeErr(w, r, http.StatusUnprocessableEntity, "secret_blocked", blocked.Error(), map[string]any{
			"findings": blocked.Findings,
		})
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	_, _ = s.bumpHead(r.Context(), name)
	w.Header().Set("ETag", etag(ver))
	s.auditEmit(r, "file.write", name, path, &audit.Diff{Ops: 1})
	writeJSON(w, http.StatusOK, map[string]any{"version": string(ver)})
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	path := r.PathValue("path")
	if !s.requireCap(w, r, fmt.Sprintf("spaces/%s/files/%s", name, path), authz.CapDelete) {
		return
	}
	expected, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	err = s.Spaces.DeleteFile(r.Context(), name, path, expected)
	if errors.Is(err, storage.ErrConflict) {
		writeErr(w, r, http.StatusPreconditionFailed, "version_conflict", err.Error(), nil)
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "not_found", "file not found", nil)
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	_, _ = s.bumpHead(r.Context(), name)
	s.auditEmit(r, "file.delete", name, path, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHead(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name+"/head", authz.CapRead) {
		return
	}
	head, err := s.Spaces.Head(r.Context(), name)
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"space": ""})
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"space": string(head)})
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name+"/files", authz.CapList) {
		return
	}
	since := r.URL.Query().Get("since")
	changes, head, err := s.Spaces.Changes(r.Context(), name, since)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"head":    string(head),
		"changes": changes,
	})
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name+"/push", authz.CapUpdate) {
		return
	}
	var req spacesvc.PushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "invalid json", nil)
		return
	}
	res, err := s.Spaces.Push(r.Context(), name, req)
	if errors.Is(err, storage.ErrConflict) {
		writeErr(w, r, http.StatusPreconditionFailed, "version_conflict", err.Error(), nil)
		return
	}
	var blocked *hooks.BlockedError
	if errors.As(err, &blocked) {
		s.auditWrite(r, "secret.blocked", name, blocked.Findings[0].Path, audit.ResultDenied, blocked.Error(), nil)
		if s.Dispatch != nil {
			s.Dispatch.Emit(webhooks.Event{
				Type:  "secret.blocked",
				Space: name,
				Actor: actorFrom(r, principalFrom(r.Context())).Username,
				Data: map[string]any{
					"path":     blocked.Findings[0].Path,
					"rule":     blocked.Findings[0].Rule,
					"findings": blocked.Findings,
				},
			})
		}
		writeErr(w, r, http.StatusUnprocessableEntity, "secret_blocked", blocked.Error(), map[string]any{
			"findings": blocked.Findings,
		})
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	ops := 0
	if res != nil {
		ops = res.Applied
	}
	s.auditEmit(r, "space.push", name, "", &audit.Diff{Ops: ops})
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name+"/history/", authz.CapList) {
		return
	}
	b, err := s.Spaces.OpenBackend(name)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	list, err := (&storage.History{Backend: b}).ListSnapshots(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list})
}

func (s *Server) handleTakeSnapshot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name+"/history/", authz.CapCreate) {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	b, err := s.Spaces.OpenBackend(name)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	meta, err := (&storage.History{Backend: b}).SnapshotSpace(r.Context(), s.Spaces.SpaceRoot(name), body.Message)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	s.auditEmit(r, "space.snapshot", name, body.Message, nil)
	writeJSON(w, http.StatusCreated, meta)
}

func (s *Server) bumpHead(ctx context.Context, name string) (storage.Version, error) {
	b, err := s.Spaces.OpenBackend(name)
	if err != nil {
		return "", err
	}
	cur, err := b.Head(ctx, storage.SpaceScope)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return "", err
	}
	if errors.Is(err, storage.ErrNotFound) {
		cur = ""
	}
	next := storage.Version(fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := b.SetHead(ctx, storage.SpaceScope, cur, next); err != nil {
		return "", err
	}
	return next, nil
}

func parseIfMatch(h string) (storage.Version, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return "", fmt.Errorf("If-Match header required (use \"\" for create)")
	}
	// strip optional quotes
	if len(h) >= 2 && h[0] == '"' && h[len(h)-1] == '"' {
		h = h[1 : len(h)-1]
	}
	return storage.Version(h), nil
}

func etag(v storage.Version) string {
	return `"` + string(v) + `"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, r *http.Request, status int, code, msg string, details any) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    msg,
			"details":    details,
			"request_id": w.Header().Get("X-Request-Id"),
		},
	})
}

// EncodeB64 is exported for clients.
func EncodeB64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
