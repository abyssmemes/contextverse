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
	pols := p.Policies
	if len(pols) == 0 && p.Role != "" {
		pols = []string{string(p.Role)}
	}
	if !s.Authz.AllowUser(p.User, pols, path, cap, s.authzVars()) {
		s.auditDenied(r, "authz.deny", "", path, fmt.Sprintf("missing %s on %s", cap, path))
		s.deny(w, r, fmt.Sprintf("missing %s on %s", cap, path))
		return false
	}
	return true
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
