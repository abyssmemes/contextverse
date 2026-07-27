// Package localui serves the web console against a solo or client space.
//
// The server's console needs a ServerConfig, an auth store and a spacesvc data
// dir; a local space has none of those. Rather than write a second console,
// this serves the **same templates** under the **same URL shapes**, presenting
// the local space as a single space named "local". One set of markup, two
// hosts — so the two cannot drift into different products.
//
// # Why this is not on by default
//
// A local console is a web server with write access to the user's context
// files, running as that user, with no account behind it. On a server that is
// the point and it sits behind login, CSRF and ACL. On a laptop it would be a
// permanently open door for someone who may never open the browser, and any
// page in any tab can try to reach 127.0.0.1. So `contextd ui` is on demand,
// and the guards below are not optional:
//
//   - bound to loopback only;
//   - a fresh token per run, exchanged once for a session cookie;
//   - Host and Origin validated, which is what stops DNS rebinding — a
//     rebound name resolves to 127.0.0.1 but still arrives with the attacker's
//     Host header;
//   - double-submit CSRF on every write.
package localui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/server/ui"
	"github.com/abyssmemes/contextverse/internal/storage"
	"github.com/abyssmemes/contextverse/internal/version"
)

const (
	sessionCookie = "cv_local"
	csrfCookie    = "cv_local_csrf"
	csrfField     = "csrf_token"
	spaceName     = "local" // the single space a local console serves
)

// Options configures a local console.
type Options struct {
	SpaceRoot string
	Addr      string // host:port; port 0 picks a free one
	FileLog   *storage.FileLog
	Mode      string
}

// Server is a local web console.
type Server struct {
	opts    Options
	token   string // one-time, exchanged for the session cookie
	session string
	ln      net.Listener
	srv     *http.Server
}

// New prepares a console and binds its listener, so the caller can print the
// real URL before serving (port 0 is only resolved by binding).
func New(opts Options) (*Server, error) {
	if opts.SpaceRoot == "" {
		return nil, errors.New("space root is required")
	}
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen address %q: %w", opts.Addr, err)
	}
	// Refusing rather than quietly rewriting: someone asking for 0.0.0.0 wants
	// the console exposed, and that is a different product decision than this
	// command makes.
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("the local console binds to loopback only (got %q) — to serve a space to other people, run contextd init server", host)
	}

	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		opts:    opts,
		token:   randomToken(),
		session: randomToken(),
		ln:      ln,
	}
	s.srv = &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

// URL is the address to open, carrying the one-time token.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/auth?t=%s", s.ln.Addr().String(), s.token)
}

// Addr is the bound address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Serve runs until the context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
	}()
	err := s.srv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", ui.Static()))
	mux.HandleFunc("GET /auth", s.handleAuth)
	mux.HandleFunc("GET /{$}", s.guard(s.handleOverview))
	mux.HandleFunc("GET /ui/spaces/local", s.guard(s.handleSpace))
	mux.HandleFunc("GET /ui/spaces/local/files/{path...}", s.guard(s.handleFile))
	mux.HandleFunc("POST /ui/spaces/local/files/{path...}", s.guard(s.handleFileSave))
	return s.secure(mux)
}

// secure applies the checks that make a loopback console safe to run.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A DNS-rebinding attack resolves an attacker-controlled name to
		// 127.0.0.1, so the connection is local but the Host header is not.
		if !hostAllowed(r.Host) {
			http.Error(w, "unexpected Host header; the local console answers on localhost only", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
}

func (s *Server) originAllowed(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return hostAllowed(u.Host)
}

// guard enforces the session, and double-submit CSRF on writes.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.session)) != 1 {
			http.Error(w, "not authorized — open the URL contextd printed when it started", http.StatusForbidden)
			return
		}
		csrf := ""
		if cc, err := r.Cookie(csrfCookie); err == nil {
			csrf = cc.Value
		}
		if csrf == "" {
			csrf = randomToken()
			s.setCookie(w, csrfCookie, csrf, false)
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
			sent := r.PostFormValue(csrfField)
			if sent == "" || subtle.ConstantTimeCompare([]byte(sent), []byte(csrf)) != 1 {
				http.Error(w, "invalid or missing CSRF token; reload the page", http.StatusForbidden)
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), csrfKey{}, csrf)))
	}
}

type csrfKey struct{}

func csrfFrom(r *http.Request) string {
	v, _ := r.Context().Value(csrfKey{}).(string)
	return v
}

// handleAuth exchanges the one-time token for a session cookie, then redirects
// so the token does not linger in the address bar or the browser history.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(s.token)) != 1 {
		http.Error(w, "bad or expired link — restart contextd ui for a new one", http.StatusForbidden)
		return
	}
	s.setCookie(w, sessionCookie, s.session, true)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: httpOnly,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) page(r *http.Request, title, active string, data any) ui.Page {
	return ui.Page{
		Title:   title,
		Active:  active,
		Version: version.Version,
		Data:    data,
		CSRF:    csrfFrom(r),
		Local:   true,
	}
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui/spaces/local", http.StatusSeeOther)
}

type spaceView struct {
	Name     string
	Head     string
	Template string
	Files    []fileRow
}

type fileRow struct {
	Path    string
	Version string
}

func (s *Server) handleSpace(w http.ResponseWriter, r *http.Request) {
	rows, err := s.listFiles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view := spaceView{Name: filepath.Base(s.opts.SpaceRoot), Files: rows, Head: s.opts.Mode}
	if cfg, err := config.Load(s.opts.SpaceRoot); err == nil {
		view.Template = cfg.Template
		if cfg.Sync.LastHead != "" {
			view.Head = cfg.Sync.LastHead
		}
	}
	if view.Template == "" {
		view.Template = "—"
	}
	_ = ui.Render(w, "space.html", s.page(r, "Space", "spaces", view))
}

func (s *Server) listFiles(ctx context.Context) ([]fileRow, error) {
	entries, err := s.opts.FileLog.Backend.List(ctx, "")
	if err != nil {
		return nil, err
	}
	var rows []fileRow
	for _, e := range entries {
		if strings.HasPrefix(e.Path, storage.SnapshotPrefix) || storage.IsFileLogInternal(e.Path) {
			continue
		}
		if strings.HasPrefix(e.Path, "_health/") || strings.HasPrefix(e.Path, "_heads/") {
			continue
		}
		ver := e.Version
		if lv, err := s.opts.FileLog.LiveVersion(ctx, e.Path); err == nil {
			ver = lv
		}
		rows = append(rows, fileRow{Path: e.Path, Version: storage.DisplayVersion(ver)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	return rows, nil
}

type fileView struct {
	Space        string
	Path         string
	Content      string
	Current      int
	Version      string
	Versions     []storage.FileVersionInfo
	Viewing      int
	VersionQ     string
	Historical   bool
	Editable     bool
	CanWrite     bool
	IsMarkdown   bool
	MarkdownHTML template.HTML
	ViewMode     string
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	s.renderFile(w, r, "")
}

func (s *Server) renderFile(w http.ResponseWriter, r *http.Request, flashErr string) {
	path := r.PathValue("path")
	ctx := r.Context()

	meta, versions, _ := s.opts.FileLog.ListVersions(ctx, path)
	current := 0
	if meta != nil {
		current = meta.Current
	}

	viewing := current
	if q := r.URL.Query().Get("version"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			viewing = n
		}
	}

	var (
		data []byte
		ver  storage.Version
		err  error
	)
	if viewing > 0 && viewing != current {
		data, _, err = s.opts.FileLog.GetVersion(ctx, path, viewing)
	} else {
		data, ver, err = s.opts.FileLog.Get(ctx, path)
	}
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	v := fileView{
		Space:      spaceName,
		Path:       path,
		Content:    string(data),
		Current:    current,
		Version:    string(ver),
		Versions:   versions,
		Viewing:    viewing,
		Historical: viewing > 0 && viewing != current,
		Editable:   true,
		CanWrite:   true,
		IsMarkdown: ui.IsMarkdownPath(path),
		ViewMode:   r.URL.Query().Get("view"),
	}
	if v.Historical {
		v.VersionQ = "?version=" + strconv.Itoa(viewing)
		v.Editable = false
	}
	if v.IsMarkdown && v.ViewMode == "preview" {
		v.MarkdownHTML = ui.RenderMarkdown([]byte(v.Content))
	}

	pg := s.page(r, path, "spaces", v)
	pg.FlashError = flashErr
	_ = ui.Render(w, "file.html", pg)
}

func (s *Server) handleFileSave(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	ctx := r.Context()
	expected := storage.Version(strings.TrimSpace(r.FormValue("version")))

	if n := strings.TrimSpace(r.FormValue("restore_version")); r.FormValue("action") == "restore" && n != "" {
		num, err := strconv.Atoi(n)
		if err != nil || num < 1 {
			s.renderFile(w, r, "invalid version to restore")
			return
		}
		body, _, err := s.opts.FileLog.GetVersion(ctx, path, num)
		if err != nil {
			s.renderFile(w, r, err.Error())
			return
		}
		if _, err := s.opts.FileLog.Put(ctx, path, body, expected); err != nil {
			s.renderFile(w, r, saveError(err))
			return
		}
		logx.L().Info("local ui restore", "path", path, "from", num)
		http.Redirect(w, r, "/ui/spaces/local/files/"+path, http.StatusSeeOther)
		return
	}

	body := []byte(strings.ReplaceAll(r.FormValue("content"), "\r\n", "\n"))
	next, err := s.opts.FileLog.Put(ctx, path, body, expected)
	if err != nil {
		s.renderFile(w, r, saveError(err))
		return
	}
	// Keep the working tree in step, exactly as the CLI does after a write —
	// otherwise the file on disk and the version log disagree.
	if err := writeTreeFile(s.opts.SpaceRoot, path, body); err != nil {
		logx.L().Warn("local ui working-tree write", "path", path, "err", err)
	}
	logx.L().Info("local ui save", "path", path, "bytes", len(body), "version", string(next))
	http.Redirect(w, r, "/ui/spaces/local/files/"+path, http.StatusSeeOther)
}

func saveError(err error) string {
	if errors.Is(err, storage.ErrConflict) {
		return "this file changed since you opened it — reload and reapply your edit"
	}
	return err.Error()
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// A predictable session token would defeat the whole guard, so failing
		// loudly beats continuing with something guessable.
		panic("localui: cannot read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// writeTreeFile mirrors a saved body into the working tree, the same step the
// CLI performs after a write so the file on disk and the version log agree.
func writeTreeFile(spaceRoot, rel string, data []byte) error {
	abs := filepath.Join(spaceRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}
