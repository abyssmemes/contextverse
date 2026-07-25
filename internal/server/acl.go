package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/authz"
)

func (s *Server) authzVars() authz.Vars {
	d := "team"
	if s.Cfg != nil && s.Cfg.Defaults.Space != "" {
		d = s.Cfg.Defaults.Space
	}
	return authz.Vars{"default": d}
}

func (s *Server) deny(w http.ResponseWriter, r *http.Request, msg string) {
	writeErr(w, r, http.StatusForbidden, "permission_denied", msg, nil)
}

func (s *Server) requireCap(w http.ResponseWriter, r *http.Request, path string, cap authz.Capability) bool {
	p := principalFrom(r.Context())
	if p == nil {
		s.deny(w, r, "unauthenticated")
		return false
	}
	if s.Authz == nil {
		// Fallback during incomplete init: legacy role checks
		return s.requireLegacy(w, r, p, path, cap)
	}
	if !s.allow(p, path, cap) {
		s.auditDenied(r, "authz.deny", "", path, fmt.Sprintf("missing %s on %s", cap, path))
		s.deny(w, r, fmt.Sprintf("missing %s on %s", cap, path))
		return false
	}
	return true
}

// requireCapUI is requireCap for the HTML console, which answers in text rather
// than the JSON error envelope.
func (s *Server) requireCapUI(w http.ResponseWriter, r *http.Request, path string, cap authz.Capability) bool {
	p := principalFrom(r.Context())
	if p == nil {
		http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
		return false
	}
	if s.allow(p, path, cap) {
		return true
	}
	s.auditDenied(r, "authz.deny", "", path, fmt.Sprintf("missing %s on %s", cap, path))
	http.Error(w, fmt.Sprintf("permission denied: missing %s on %s", cap, path), http.StatusForbidden)
	return false
}

// allow answers the policy question without writing a response, so listings can
// filter instead of failing.
func (s *Server) allow(p *auth.Principal, path string, cap authz.Capability) bool {
	if p == nil {
		return false
	}
	if s.Authz == nil {
		switch {
		case strings.HasPrefix(path, "sys/") && cap != authz.CapRead:
			return auth.CanAdmin(p.Role)
		case cap == authz.CapUpdate || cap == authz.CapCreate || cap == authz.CapDelete:
			return auth.CanWrite(p.Role)
		default:
			return true
		}
	}
	pols := p.Policies
	if len(pols) == 0 && p.Role != "" {
		pols = []string{string(p.Role)}
	}
	return s.Authz.AllowUser(p.User, pols, path, cap, s.authzVars())
}

// visibleSpaces keeps the spaces a principal may actually look at. Listing every
// space name to everyone leaks the shape of the deployment (and, in a pooled
// server, of other tenants).
func (s *Server) visibleSpaces(p *auth.Principal, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if s.canSeeSpace(p, n) {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) canSeeSpace(p *auth.Principal, name string) bool {
	return s.allow(p, "spaces/"+name, authz.CapRead) ||
		s.allow(p, "spaces/"+name+"/files", authz.CapList)
}

// eventScopesFor narrows an SSE subscription to the spaces the caller may read.
// An empty request means "everything", which for a scoped principal must mean
// "everything I can see" — not the whole server.
func (s *Server) eventScopesFor(p *auth.Principal, requested []string) []string {
	names, err := s.Spaces.List()
	if err != nil {
		return nil
	}
	visible := s.visibleSpaces(p, names)
	if len(visible) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(visible))
	for _, n := range visible {
		allowed[n] = true
	}
	if len(requested) == 0 {
		return visible
	}
	out := make([]string, 0, len(requested))
	for _, scope := range requested {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if scope == "*" {
			return visible
		}
		space := strings.TrimSuffix(scope, "/")
		if i := strings.Index(space, "/"); i >= 0 {
			space = space[:i]
		}
		if allowed[space] {
			out = append(out, scope)
		}
	}
	return out
}

func (s *Server) requireLegacy(w http.ResponseWriter, r *http.Request, p *auth.Principal, path string, cap authz.Capability) bool {
	switch {
	case strings.HasPrefix(path, "sys/") && cap != authz.CapRead:
		if !auth.CanAdmin(p.Role) {
			s.deny(w, r, "admin role required")
			return false
		}
	case cap == authz.CapUpdate || cap == authz.CapCreate || cap == authz.CapDelete:
		if !auth.CanWrite(p.Role) {
			s.deny(w, r, "write role required")
			return false
		}
	}
	return true
}

func (s *Server) requireFileWrite(w http.ResponseWriter, r *http.Request, space, filePath string) bool {
	if s.canFileWrite(principalFrom(r.Context()), space, filePath) {
		return true
	}
	s.deny(w, r, fmt.Sprintf("missing create/update on spaces/%s/files/%s", space, strings.TrimPrefix(filePath, "/")))
	return false
}

func (s *Server) canFileWrite(p *auth.Principal, space, filePath string) bool {
	if p == nil {
		return false
	}
	acl := fmt.Sprintf("spaces/%s/files/%s", space, strings.TrimPrefix(filePath, "/"))
	if s.Authz == nil {
		return auth.CanWrite(p.Role)
	}
	pols := p.Policies
	if len(pols) == 0 && p.Role != "" {
		pols = []string{string(p.Role)}
	}
	vars := s.authzVars()
	return s.Authz.AllowUser(p.User, pols, acl, authz.CapUpdate, vars) || s.Authz.AllowUser(p.User, pols, acl, authz.CapCreate, vars)
}
