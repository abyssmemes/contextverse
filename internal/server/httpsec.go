package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/orkcom-tech/contextverse/internal/server/ui"
)

// maxJSONBody caps request bodies for everything except file uploads and
// pushes, which stream content the operator explicitly asked to store. Without
// a cap an unauthenticated caller can make the server buffer as much as it can
// send.
const maxJSONBody int64 = 8 << 20

// csrfCookie holds the console's anti-forgery token. It is HttpOnly: the value
// the browser sends back comes from the server-rendered form, not from script,
// so nothing needs to read the cookie.
const csrfCookie = "cv_csrf"

// csrfField and csrfHeader are the two places a form value may arrive from.
const (
	csrfField  = "csrf_token"
	csrfHeader = "X-CSRF-Token"
)

type secCtxKey int

const (
	nonceKey secCtxKey = iota
	csrfKey
)

func randomToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func nonceFrom(ctx context.Context) string {
	v, _ := ctx.Value(nonceKey).(string)
	return v
}

func csrfFrom(ctx context.Context) string {
	v, _ := ctx.Value(csrfKey).(string)
	return v
}

// isAPI reports whether the request targets the JSON API rather than the
// server-rendered console. The two get different content policies.
func isAPI(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/api/") || p == "/metrics" || p == "/healthz" || p == "/readyz"
}

// withSecurityHeaders sets the response headers that keep the browser honest.
// The console is server-rendered htmx: scripts are allowed only from our own
// origin or by carrying the per-response nonce, which is what stops an injected
// tag from executing.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	apiCSP := strings.Join([]string{
		"default-src 'none'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'none'",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		if s.servesTLS() {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if isAPI(r) {
			h.Set("Content-Security-Policy", apiCSP)
			next.ServeHTTP(w, r)
			return
		}
		nonce := randomToken()
		h.Set("Content-Security-Policy", consoleCSP(nonce))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey, nonce)))
	})
}

// consoleCSP allows the webfonts the console links to, but no third-party
// script origin: cross-origin scripts have to carry the nonce, which an
// injected tag cannot guess.
func consoleCSP(nonce string) string {
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'nonce-" + nonce + "'",
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' data: https://fonts.gstatic.com",
		"img-src 'self' data:",
		"connect-src 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
	}, "; ")
}

// withCSRF gives the console a double-submit token. The cookie is minted on the
// first page load; every form echoes the value back, so a cross-site POST — which
// can carry the cookie but cannot read the page — fails the comparison.
//
// The JSON API is exempt: it authenticates with a bearer header only, which a
// foreign page cannot attach.
func (s *Server) withCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPI(r) {
			next.ServeHTTP(w, r)
			return
		}
		token := ""
		if c, err := r.Cookie(csrfCookie); err == nil && c.Value != "" {
			token = c.Value
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			if token == "" {
				token = randomToken()
				s.setCSRFCookie(w, token)
			}
		default:
			if token == "" || !csrfMatches(r, token) {
				http.Error(w, "invalid or missing CSRF token; reload the page and try again", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), csrfKey, token)))
	})
}

func (s *Server) setCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.servesTLS(),
		MaxAge:   60 * 60 * 24 * 7,
	})
}

// csrfMatches compares the submitted token without leaking its length or
// contents through timing.
func csrfMatches(r *http.Request, want string) bool {
	if sent := r.Header.Get(csrfHeader); sent != "" {
		return subtle.ConstantTimeCompare([]byte(sent), []byte(want)) == 1
	}
	// ParseForm consumes the body; handlers call it again, which is a no-op
	// once r.PostForm is populated.
	if err := r.ParseForm(); err != nil {
		return false
	}
	sent := r.PostFormValue(csrfField)
	return sent != "" && subtle.ConstantTimeCompare([]byte(sent), []byte(want)) == 1
}

// render stamps the per-request nonce and CSRF token onto the view model so no
// handler can forget them, then writes the page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, pg ui.Page) error {
	pg.Nonce = nonceFrom(r.Context())
	pg.CSRF = csrfFrom(r.Context())
	return ui.Render(w, name, pg)
}

// withBodyLimit caps request bodies. File writes and pushes are exempt: their
// size is governed by the space quotas instead.
func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !streamsContent(r) {
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
		}
		next.ServeHTTP(w, r)
	})
}

func streamsContent(r *http.Request) bool {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		return false
	}
	p := r.URL.Path
	return strings.Contains(p, "/files/") ||
		strings.HasSuffix(p, "/push") ||
		strings.Contains(p, "/undelete/")
}
