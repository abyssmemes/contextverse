package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/abyssmemes/contextverse/internal/audit"
	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/server/ui"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
	"github.com/abyssmemes/contextverse/internal/storage"
	"github.com/abyssmemes/contextverse/internal/version"
	"github.com/abyssmemes/contextverse/internal/webhooks"
)

const sessionCookie = "cv_session"

func (s *Server) registerUI(mux *http.ServeMux) {
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", ui.Static()))

	if s.NeedsSetup {
		mux.HandleFunc("GET /{$}", s.handleSetupGet)
		mux.HandleFunc("GET /setup", s.handleSetupGet)
		mux.HandleFunc("POST /setup", s.handleSetupPost)
		return
	}

	mux.HandleFunc("GET /{$}", s.handleUIHome)
	mux.HandleFunc("GET /ui/login", s.handleLoginGet)
	mux.HandleFunc("POST /ui/login", s.handleLoginPost)
	mux.HandleFunc("GET /ui/logout", s.handleLogout)
	mux.Handle("GET /ui/spaces", s.uiAuth(s.handleUISpaces))
	mux.Handle("POST /ui/spaces", s.uiAuth(s.handleUICreateSpace))
	mux.Handle("GET /ui/spaces/{space}", s.uiAuth(s.handleUISpace))
	mux.Handle("GET /ui/spaces/{space}/files/{path...}", s.uiAuth(s.handleUIFile))
	mux.Handle("POST /ui/spaces/{space}/files/{path...}", s.uiAuth(s.handleUIFileSave))
	mux.Handle("GET /ui/users", s.uiAuth(s.handleUIUsers))
	mux.Handle("POST /ui/users", s.uiAuth(s.handleUIAddUser))
	mux.Handle("GET /ui/backends", s.uiAuth(s.handleUIBackends))
	mux.Handle("POST /ui/backends/set", s.uiAuth(s.handleUIBackendSet))
	mux.Handle("POST /ui/backends/test", s.uiAuth(s.handleUIBackendTest))
	mux.Handle("POST /ui/backends/migrate", s.uiAuth(s.handleUIBackendMigrate))
	mux.Handle("GET /ui/policies", s.uiAuth(s.handleUIPolicies))
	mux.Handle("GET /ui/policies/{name}", s.uiAuth(s.handleUIPolicyShow))
	mux.Handle("POST /ui/policies", s.uiAuth(s.handleUIPolicyWrite))
	mux.Handle("GET /ui/audit", s.uiAuth(s.handleUIAudit))
	mux.Handle("GET /ui/audit/export", s.uiAuth(s.handleUIAuditExport))
	mux.Handle("GET /ui/webhooks", s.uiAuth(s.handleUIWebhooks))
	mux.Handle("POST /ui/webhooks", s.uiAuth(s.handleUIWebhooksCreate))
	mux.Handle("POST /ui/webhooks/{id}/test", s.uiAuth(s.handleUIWebhooksTest))
	mux.Handle("POST /ui/webhooks/{id}/delete", s.uiAuth(s.handleUIWebhooksDelete))
}

func (s *Server) pageBase(active string, p *auth.Principal) ui.Page {
	pg := ui.Page{Active: active, Version: version.Version}
	if p != nil {
		pg.User = p.User
		pg.Role = string(p.Role)
	}
	return pg
}

func (s *Server) uiAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := s.principalFromRequest(r)
		if p == nil {
			http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

func (s *Server) principalFromRequest(r *http.Request) *auth.Principal {
	if s.Auth == nil {
		return nil
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if p, err := s.Auth.Authenticate(c.Value); err == nil {
			return p
		}
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		if p, err := s.Auth.Authenticate(strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))); err == nil {
			return p
		}
	}
	return nil
}

func (s *Server) setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 30,
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
}

func (s *Server) handleSetupGet(w http.ResponseWriter, r *http.Request) {
	pg := ui.Page{
		Title:   "Setup",
		Version: version.Version,
		Data:    s.setupDefaults(nil),
	}
	_ = ui.Render(w, "setup.html", pg)
}

func (s *Server) setupDefaults(override map[string]any) map[string]any {
	d := map[string]any{
		"DataDir":  s.setupDataDir,
		"Address":  s.setupAddr,
		"Port":     s.setupPort,
		"Space":    "team",
		"Admin":    "admin",
		"Template": "solo-default",
		"Backend":  "local",
		"Step":     1,
	}
	for k, v := range override {
		d[k] = v
	}
	return d
}

func (s *Server) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.setupErr(w, r, "invalid form", 1)
		return
	}
	dataDir := strings.TrimSpace(r.FormValue("data_dir"))
	address := strings.TrimSpace(r.FormValue("address"))
	port, _ := strconv.Atoi(r.FormValue("port"))
	spaceName := strings.TrimSpace(r.FormValue("space"))
	adminName := strings.TrimSpace(r.FormValue("admin"))
	templateName := strings.TrimSpace(r.FormValue("template"))
	backend := strings.TrimSpace(r.FormValue("backend"))
	if dataDir == "" || address == "" || port == 0 {
		s.setupErr(w, r, "server location fields are required", 1)
		return
	}
	if spaceName == "" || adminName == "" {
		s.setupErr(w, r, "admin and space are required", 2)
		return
	}
	if templateName == "" {
		templateName = "solo-default"
	}
	if backend == "" {
		backend = "local"
	}
	if config.ServerExists(dataDir) {
		s.setupErr(w, r, "server already initialized at "+dataDir, 1)
		return
	}

	cfg := &config.ServerConfig{
		Mode:     config.ModeServer,
		DataDir:  dataDir,
		Listen:   config.ListenConfig{Address: address, Port: port},
		Backend:  backendFromForm(r, config.Backend{Driver: backend}),
		Defaults: config.ServerDefaults{Space: spaceName},
	}
	if err := config.SaveServer(cfg); err != nil {
		s.setupErr(w, r, err.Error(), 4)
		return
	}
	store, err := auth.OpenStore(dataDir)
	if err != nil {
		s.setupErr(w, r, err.Error(), 4)
		return
	}
	if err := store.AddUser(adminName, auth.RoleAdmin); err != nil {
		s.setupErr(w, r, err.Error(), 2)
		return
	}
	if pw := r.FormValue("password"); strings.TrimSpace(pw) != "" {
		if err := store.SetPassword(adminName, pw); err != nil {
			s.setupErr(w, r, err.Error(), 2)
			return
		}
	}
	token, _, err := store.CreateToken(adminName, "setup-ui")
	if err != nil {
		s.setupErr(w, r, err.Error(), 4)
		return
	}
	svc := &spacesvc.Service{DataDir: dataDir, Backend: cfg.Backend}
	if _, err := svc.Create(r.Context(), spaceName, templateName, true); err != nil {
		s.setupErr(w, r, err.Error(), 4)
		return
	}

	eng, _ := authz.Open(store.PoliciesDir())
	s.mu.Lock()
	s.Cfg = cfg
	s.Auth = store
	s.Authz = eng
	s.Spaces = svc
	s.NeedsSetup = false
	s.setupDataDir = dataDir
	s.mu.Unlock()

	logx.L().Info("ui setup complete", "data_dir", dataDir, "space", spaceName)
	s.setSession(w, token)
	pg := ui.Page{
		Title:   "Installed",
		Version: version.Version,
		Data: map[string]any{
			"Token":   token,
			"Space":   spaceName,
			"Listen":  cfg.Addr(),
			"DataDir": dataDir,
		},
	}
	_ = ui.Render(w, "setup_done.html", pg)
}

func (s *Server) setupErr(w http.ResponseWriter, r *http.Request, msg string, step int) {
	if step < 1 {
		step = 1
	}
	vals := s.setupDefaults(map[string]any{
		"Step":       step,
		"DataDir":    strings.TrimSpace(r.FormValue("data_dir")),
		"Address":    strings.TrimSpace(r.FormValue("address")),
		"Port":       r.FormValue("port"),
		"Space":      strings.TrimSpace(r.FormValue("space")),
		"Admin":      strings.TrimSpace(r.FormValue("admin")),
		"Template":   strings.TrimSpace(r.FormValue("template")),
		"Backend":    strings.TrimSpace(r.FormValue("backend")),
		"GitRemote":  r.FormValue("git_remote"),
		"GitUser":    r.FormValue("git_user"),
		"GitSSHKey":  r.FormValue("git_ssh_key"),
		"S3Endpoint": r.FormValue("s3_endpoint"),
		"S3Bucket":   r.FormValue("s3_bucket"),
		"S3Region":   r.FormValue("s3_region"),
		"S3Prefix":   r.FormValue("s3_prefix"),
		"S3AccessKey": r.FormValue("s3_access_key"),
	})
	if vals["DataDir"] == "" {
		vals["DataDir"] = s.setupDataDir
	}
	if vals["Address"] == "" {
		vals["Address"] = s.setupAddr
	}
	if vals["Port"] == "" || vals["Port"] == "0" {
		vals["Port"] = s.setupPort
	}
	if vals["Space"] == "" {
		vals["Space"] = "team"
	}
	if vals["Admin"] == "" {
		vals["Admin"] = "admin"
	}
	if vals["Template"] == "" {
		vals["Template"] = "solo-default"
	}
	if vals["Backend"] == "" {
		vals["Backend"] = "local"
	}
	pg := ui.Page{
		Title:      "Setup",
		Version:    version.Version,
		FlashError: msg,
		Data:       vals,
	}
	w.WriteHeader(http.StatusBadRequest)
	_ = ui.Render(w, "setup.html", pg)
}

func (s *Server) handleUIHome(w http.ResponseWriter, r *http.Request) {
	p := s.principalFromRequest(r)
	if p == nil {
		http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
		return
	}
	spaces, _ := s.Spaces.List()
	users, _ := s.Auth.ListUsers()
	driver := s.Cfg.Backend.Driver
	if driver == "" {
		driver = "local"
	}
	pg := s.pageBase("dash", p)
	pg.Title = "Dashboard"
	pg.Data = map[string]any{
		"Spaces":       len(spaces),
		"Users":        len(users),
		"Backend":      driver,
		"Listen":       s.Cfg.Addr(),
		"DataDir":      s.Cfg.DataDir,
		"DefaultSpace": s.Cfg.Defaults.Space,
	}
	_ = ui.Render(w, "dashboard.html", pg)
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	_ = ui.Render(w, "login.html", ui.Page{Title: "Login", Version: version.Version})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	token := strings.TrimSpace(r.FormValue("token"))
	user := strings.TrimSpace(r.FormValue("username"))
	pass := r.FormValue("password")
	var (
		p   *auth.Principal
		err error
	)
	if user != "" && pass != "" {
		tok, _, loginErr := s.Auth.LoginUserpass(user, pass)
		if loginErr != nil {
			_ = ui.Render(w, "login.html", ui.Page{Title: "Login", Version: version.Version, FlashError: loginErr.Error()})
			return
		}
		token = tok
	}
	if token == "" {
		_ = ui.Render(w, "login.html", ui.Page{Title: "Login", Version: version.Version, FlashError: "username/password or token required"})
		return
	}
	p, err = s.Auth.Authenticate(token)
	if err != nil {
		_ = ui.Render(w, "login.html", ui.Page{Title: "Login", Version: version.Version, FlashError: "invalid token"})
		return
	}
	s.setSession(w, token)
	logx.L().Info("ui login", "user", p.User)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w)
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}

func (s *Server) handleUISpaces(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	names, err := s.Spaces.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type row struct{ Name, Head string }
	var rows []row
	for _, n := range names {
		h, _ := s.Spaces.Head(r.Context(), n)
		rows = append(rows, row{Name: n, Head: string(h)})
	}
	pg := s.pageBase("spaces", p)
	pg.Title = "Spaces"
	pg.Data = map[string]any{"Spaces": rows}
	_ = ui.Render(w, "spaces.html", pg)
}

func (s *Server) handleUICreateSpace(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !auth.CanAdmin(p.Role) {
		http.Error(w, "admin only", 403)
		return
	}
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	tpl := strings.TrimSpace(r.FormValue("template"))
	if _, err := s.Spaces.Create(r.Context(), name, tpl, false); err != nil {
		pg := s.pageBase("spaces", p)
		pg.Title = "Spaces"
		pg.FlashError = err.Error()
		names, _ := s.Spaces.List()
		type row struct{ Name, Head string }
		var rows []row
		for _, n := range names {
			h, _ := s.Spaces.Head(r.Context(), n)
			rows = append(rows, row{Name: n, Head: string(h)})
		}
		pg.Data = map[string]any{"Spaces": rows}
		_ = ui.Render(w, "spaces.html", pg)
		return
	}
	http.Redirect(w, r, "/ui/spaces/"+name, http.StatusSeeOther)
}

func (s *Server) handleUISpace(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	name := r.PathValue("space")
	meta, err := s.Spaces.LoadMeta(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	head, _ := s.Spaces.Head(r.Context(), name)
	files, _ := s.Spaces.Tree(r.Context(), name)
	pg := s.pageBase("spaces", p)
	pg.Title = name
	pg.Data = map[string]any{
		"Name":     name,
		"Head":     string(head),
		"Template": meta.Template,
		"Files":    files,
	}
	_ = ui.Render(w, "space.html", pg)
}

func (s *Server) handleUIFile(w http.ResponseWriter, r *http.Request) {
	s.renderUIFile(w, r, "", "")
}

func (s *Server) handleUIFileSave(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	spaceName := r.PathValue("space")
	path := r.PathValue("path")
	if !s.canFileWrite(p, spaceName, path) {
		s.renderUIFile(w, r, "", "permission denied — your policy cannot write this path")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderUIFile(w, r, "", "invalid form")
		return
	}
	expected := storage.Version(strings.TrimSpace(r.FormValue("version")))

	if r.FormValue("action") == "restore" {
		n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("restore_version")))
		if err != nil || n < 1 {
			s.renderUIFile(w, r, "", "invalid restore version")
			return
		}
		data, _, err := s.Spaces.GetFileVersion(r.Context(), spaceName, path, n)
		if errors.Is(err, storage.ErrNotFound) {
			s.renderUIFile(w, r, "", "version not found")
			return
		}
		if err != nil {
			s.renderUIFile(w, r, "", err.Error())
			return
		}
		ver, err := s.Spaces.PutFile(r.Context(), spaceName, path, data, expected)
		if errors.Is(err, storage.ErrConflict) {
			s.renderUIFile(w, r, "", "version conflict — reload and try again")
			return
		}
		if err != nil {
			s.renderUIFile(w, r, "", err.Error())
			return
		}
	_, _ = s.bumpHead(r.Context(), spaceName)
	logx.L().Info("ui file restored", "space", spaceName, "path", path, "from", n, "user", p.User, "version", string(ver))
	s.auditEmit(r, "file.write", spaceName, path, &audit.Diff{Ops: 1})
	http.Redirect(w, r, "/ui/spaces/"+spaceName+"/files/"+path, http.StatusSeeOther)
		return
	}

	content := r.FormValue("content")
	ver, err := s.Spaces.PutFile(r.Context(), spaceName, path, []byte(content), expected)
	if errors.Is(err, storage.ErrConflict) {
		s.renderUIFile(w, r, "", "version conflict — reload and try again")
		return
	}
	if err != nil {
		s.renderUIFile(w, r, "", err.Error())
		return
	}
	_, _ = s.bumpHead(r.Context(), spaceName)
	logx.L().Info("ui file saved", "space", spaceName, "path", path, "user", p.User, "version", string(ver))
	s.auditEmit(r, "file.write", spaceName, path, &audit.Diff{Ops: 1})
	http.Redirect(w, r, "/ui/spaces/"+spaceName+"/files/"+path+"?flash=saved", http.StatusSeeOther)
}

func (s *Server) renderUIFile(w http.ResponseWriter, r *http.Request, flash, flashErr string) {
	p := principalFrom(r.Context())
	spaceName := r.PathValue("space")
	path := r.PathValue("path")
	if flash == "" {
		flash = r.URL.Query().Get("flash")
	}

	meta, versions, _ := s.Spaces.ListFileVersions(r.Context(), spaceName, path)
	current := 0
	if meta != nil {
		current = meta.Current
	}

	var (
		data    []byte
		ver     storage.Version
		err     error
		viewing int
	)
	if vq := strings.TrimSpace(r.URL.Query().Get("version")); vq != "" {
		n, aerr := strconv.Atoi(vq)
		if aerr != nil || n < 1 {
			http.Error(w, "invalid version", http.StatusBadRequest)
			return
		}
		var info *storage.FileVersionInfo
		data, info, err = s.Spaces.GetFileVersion(r.Context(), spaceName, path, n)
		if err == nil && info != nil {
			viewing = info.Version
			ver = storage.FormatFileVersion(info.Version)
		}
	} else {
		data, ver, err = s.Spaces.GetFile(r.Context(), spaceName, path)
	}
	if errors.Is(err, storage.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	historical := viewing > 0 && viewing != current
	canWrite := s.canFileWrite(p, spaceName, path)
	editable := utf8.Valid(data) && !historical && canWrite
	isMD := isMarkdownPath(path) && utf8.Valid(data)
	viewMode := strings.TrimSpace(r.URL.Query().Get("view"))
	if viewMode != "preview" && viewMode != "edit" {
		if historical && isMD {
			viewMode = "preview"
		} else {
			viewMode = "edit"
		}
	}
	if !editable && isMD {
		viewMode = "preview"
	}

	pg := s.pageBase("spaces", p)
	pg.Title = path
	pg.Flash = flash
	pg.FlashError = flashErr
	pg.Data = map[string]any{
		"Space":        spaceName,
		"Path":         path,
		"Version":      string(ver),
		"Current":      current,
		"Viewing":      viewing,
		"Historical":   historical,
		"Content":      string(data),
		"CanWrite":     canWrite,
		"Editable":     editable,
		"Versions":     versions,
		"IsMarkdown":   isMD,
		"ViewMode":     viewMode,
		"MarkdownHTML": renderMarkdownHTML(data),
		"VersionQ":     r.URL.Query().Get("version"),
	}
	_ = ui.Render(w, "file.html", pg)
}

func (s *Server) handleUIUsers(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	users, err := s.Auth.ListUsers()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	pg := s.pageBase("users", p)
	pg.Title = "Users"
	pg.Data = map[string]any{"Users": users, "NewToken": ""}
	_ = ui.Render(w, "users.html", pg)
}

func (s *Server) handleUIAddUser(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !auth.CanAdmin(p.Role) {
		http.Error(w, "admin only", 403)
		return
	}
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	role := auth.Role(strings.TrimSpace(r.FormValue("role")))
	if err := s.Auth.AddUser(name, role); err != nil {
		pg := s.pageBase("users", p)
		pg.FlashError = err.Error()
		users, _ := s.Auth.ListUsers()
		pg.Data = map[string]any{"Users": users}
		_ = ui.Render(w, "users.html", pg)
		return
	}
	token, _, _ := s.Auth.CreateToken(name, "ui")
	users, _ := s.Auth.ListUsers()
	pg := s.pageBase("users", p)
	pg.Title = "Users"
	pg.Flash = "user created"
	pg.Data = map[string]any{"Users": users, "NewToken": token}
	s.auditEmit(r, "user.add", "", name+":"+string(role), nil)
	_ = ui.Render(w, "users.html", pg)
}

func (s *Server) handleUIBackends(w http.ResponseWriter, r *http.Request) {
	s.renderBackends(w, r, "", "")
}

func (s *Server) handleUIBackendSet(w http.ResponseWriter, r *http.Request) {
	if s.requireUIAdmin(w, r) == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderBackends(w, r, "", "invalid form")
		return
	}
	next := backendFromForm(r, s.Cfg.Backend)
	if err := s.applyBackend(next); err != nil {
		s.renderBackends(w, r, "", err.Error())
		return
	}
	s.auditEmit(r, "backend.set", "", next.Driver, nil)
	s.renderBackends(w, r, "backend saved: "+next.Driver, "")
}

func (s *Server) handleUIBackendTest(w http.ResponseWriter, r *http.Request) {
	if s.requireUIAdmin(w, r) == nil {
		return
	}
	space := s.Cfg.Defaults.Space
	if space == "" {
		space = "team"
	}
	names, _ := s.Spaces.List()
	if len(names) > 0 {
		space = names[0]
	}
	b, err := storage.Open(storage.OpenOptions{
		SpaceRoot: s.Spaces.SpaceRoot(space),
		SpaceName: space,
		Backend:   s.Cfg.Backend,
		Driver:    s.Cfg.Backend.Driver,
	})
	if err != nil {
		s.renderBackends(w, r, "", err.Error())
		return
	}
	if err := testBackendCAS(r, b); err != nil {
		s.renderBackends(w, r, "", err.Error())
		return
	}
	s.renderBackends(w, r, "ok: driver="+b.Name()+" cas=pass", "")
}

func (s *Server) handleUIBackendMigrate(w http.ResponseWriter, r *http.Request) {
	if s.requireUIAdmin(w, r) == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderBackends(w, r, "", "invalid form")
		return
	}
	if r.FormValue("confirm") != "migrate" {
		s.renderBackends(w, r, "", "confirm checkbox required")
		return
	}
	to := backendFromForm(r, s.Cfg.Backend)
	n, err := s.migrateAllSpaces(r, to)
	if err != nil {
		s.renderBackends(w, r, "", err.Error())
		return
	}
	s.auditEmit(r, "backend.migrate", "", to.Driver, &audit.Diff{Ops: n})
	s.renderBackends(w, r, fmt.Sprintf("migrated %d objects → %s", n, to.Driver), "")
}

func (s *Server) renderBackends(w http.ResponseWriter, r *http.Request, flash, flashErr string) {
	p := principalFrom(r.Context())
	driver := s.Cfg.Backend.Driver
	if driver == "" {
		driver = "local"
	}
	pg := s.pageBase("backends", p)
	pg.Title = "Backends"
	pg.Flash = flash
	pg.FlashError = flashErr
	pg.Data = map[string]any{
		"Driver":      driver,
		"Drivers":     storage.KnownDrivers(),
		"GitRemote":   s.Cfg.Backend.GitRemote,
		"GitUser":     s.Cfg.Backend.GitUser,
		"GitSSHKey":   s.Cfg.Backend.GitSSHKey,
		"HasGitToken": s.Cfg.Backend.GitToken != "",
		"S3Bucket":    s.Cfg.Backend.S3Bucket,
		"S3Endpoint":  s.Cfg.Backend.S3Endpoint,
		"S3Region":    s.Cfg.Backend.S3Region,
		"S3Prefix":    s.Cfg.Backend.S3Prefix,
		"S3AccessKey": s.Cfg.Backend.S3AccessKey,
		"HasS3Secret": s.Cfg.Backend.S3SecretKey != "",
		"SQLDSN":      s.Cfg.Backend.SQLDSN != "",
		"Status":      s.probeBackendStatus(r),
	}
	_ = ui.Render(w, "backends.html", pg)
}

func (s *Server) handleUIPolicies(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if s.Authz == nil {
		http.Error(w, "authz not loaded", 500)
		return
	}
	type row struct {
		Name, Description string
		Builtin           bool
	}
	var rows []row
	for _, name := range s.Authz.List() {
		pol, _ := s.Authz.Get(name)
		if pol == nil {
			continue
		}
		rows = append(rows, row{Name: pol.Name, Description: pol.Description, Builtin: pol.Builtin})
	}
	def := s.Cfg.Defaults.Space
	if def == "" {
		def = "team"
	}
	pg := s.pageBase("policies", p)
	pg.Title = "Policies"
	pg.Data = map[string]any{"Policies": rows, "DefaultSpace": def}
	_ = ui.Render(w, "policies.html", pg)
}

func (s *Server) handleUIPolicyShow(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	name := r.PathValue("name")
	if s.Authz == nil {
		http.Error(w, "authz not loaded", 500)
		return
	}
	pol, ok := s.Authz.Get(name)
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	raw, _ := yaml.Marshal(pol)
	pg := s.pageBase("policies", p)
	pg.Title = "Policy " + name
	pg.Data = map[string]any{"Name": name, "Body": string(raw)}
	_ = ui.Render(w, "policy_show.html", pg)
}

func (s *Server) handleUIPolicyWrite(w http.ResponseWriter, r *http.Request) {
	if s.requireUIAdmin(w, r) == nil {
		return
	}
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	body := strings.TrimSpace(r.FormValue("body"))
	var pol authz.Policy
	if err := yaml.Unmarshal([]byte(body), &pol); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if pol.Name == "" {
		pol.Name = name
	}
	if err := s.Authz.Write(pol); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	logx.L().Info("policy written", "name", pol.Name, "user", principalFrom(r.Context()).User)
	s.auditEmit(r, "policy.write", "", pol.Name, nil)
	http.Redirect(w, r, "/ui/policies/"+pol.Name, http.StatusSeeOther)
}

func (s *Server) handleUIAudit(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !s.canAuditRead(p) {
		http.Error(w, "missing read/list on sys/audit", http.StatusForbidden)
		return
	}
	f := audit.Filter{
		Actor:  r.URL.Query().Get("actor"),
		Action: r.URL.Query().Get("action"),
		Limit:  100,
	}
	since := r.URL.Query().Get("since")
	if since == "" {
		since = "7d"
	}
	if ts, err := audit.ParseSince(since); err == nil {
		f.Since = ts
	}
	var entries []audit.Entry
	var st audit.Stats
	if s.Audit != nil {
		entries, _ = s.Audit.Query(f)
		st, _ = s.Audit.Stats(f)
	}
	pg := s.pageBase("audit", p)
	pg.Title = "Audit"
	pg.Data = map[string]any{
		"Entries": entries,
		"Stats":   st,
		"Since":   since,
		"Actor":   f.Actor,
		"Action":  f.Action,
	}
	_ = ui.Render(w, "audit.html", pg)
}

func (s *Server) handleUIAuditExport(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !s.canAuditRead(p) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f := audit.Filter{Limit: -1}
	if since := r.URL.Query().Get("since"); since != "" {
		if ts, err := audit.ParseSince(since); err == nil {
			f.Since = ts
		}
	}
	format := r.URL.Query().Get("format")
	if s.Audit == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
		_ = s.Audit.ExportCSV(w, f)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.jsonl"`)
	_ = s.Audit.ExportJSONL(w, f)
}

func (s *Server) canAuditRead(p *auth.Principal) bool {
	if p == nil {
		return false
	}
	if s.Authz == nil {
		return auth.CanAdmin(p.Role)
	}
	pols := p.Policies
	if len(pols) == 0 && p.Role != "" {
		pols = []string{string(p.Role)}
	}
	return s.Authz.Allow(pols, "sys/audit", authz.CapList, s.authzVars()) ||
		s.Authz.Allow(pols, "sys/audit", authz.CapRead, s.authzVars())
}

func (s *Server) handleUIWebhooks(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if s.requireUIAdmin(w, r) == nil {
		return
	}
	s.renderUIWebhooks(w, r, p, "", "", "")
}

func (s *Server) handleUIWebhooksCreate(w http.ResponseWriter, r *http.Request) {
	p := s.requireUIAdmin(w, r)
	if p == nil {
		return
	}
	_ = r.ParseForm()
	evRaw := strings.TrimSpace(r.FormValue("events"))
	var ev []string
	if evRaw != "" && evRaw != "*" {
		for _, x := range strings.Split(evRaw, ",") {
			x = strings.TrimSpace(x)
			if x != "" {
				ev = append(ev, x)
			}
		}
	}
	h, err := s.Hooks.Upsert(webhooks.Hook{
		URL:     strings.TrimSpace(r.FormValue("url")),
		Events:  ev,
		Space:   strings.TrimSpace(r.FormValue("space")),
		Enabled: true,
	})
	if err != nil {
		s.renderUIWebhooks(w, r, p, "", "", err.Error())
		return
	}
	s.auditEmit(r, "webhook.create", h.Space, h.ID, nil)
	s.renderUIWebhooks(w, r, p, h.ID, h.Secret, "")
}

func (s *Server) handleUIWebhooksTest(w http.ResponseWriter, r *http.Request) {
	if s.requireUIAdmin(w, r) == nil {
		return
	}
	id := r.PathValue("id")
	if err := s.Dispatch.Test(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	s.auditEmit(r, "webhook.test", "", id, nil)
	http.Redirect(w, r, "/ui/webhooks", http.StatusSeeOther)
}

func (s *Server) handleUIWebhooksDelete(w http.ResponseWriter, r *http.Request) {
	if s.requireUIAdmin(w, r) == nil {
		return
	}
	id := r.PathValue("id")
	_ = s.Hooks.Delete(id)
	s.auditEmit(r, "webhook.delete", "", id, nil)
	http.Redirect(w, r, "/ui/webhooks", http.StatusSeeOther)
}

func (s *Server) renderUIWebhooks(w http.ResponseWriter, r *http.Request, p *auth.Principal, newID, newSecret, flashErr string) {
	list, _ := s.Hooks.List()
	dl, _ := s.Hooks.ListDeadLetter(20)
	dlJSON, _ := json.MarshalIndent(dl, "", "  ")
	pg := s.pageBase("webhooks", p)
	pg.Title = "Webhooks"
	if flashErr != "" {
		pg.FlashError = flashErr
	}
	pg.Data = map[string]any{
		"Hooks":      list,
		"NewID":      newID,
		"NewSecret":  newSecret,
		"DeadLetter": string(dlJSON),
	}
	_ = ui.Render(w, "webhooks.html", pg)
}

