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
	if !s.Authz.Allow(pols, path, cap, s.authzVars()) {
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
	acl := fmt.Sprintf("spaces/%s/files/%s", space, strings.TrimPrefix(filePath, "/"))
	p := principalFrom(r.Context())
	if p == nil || s.Authz == nil {
		return s.requireCap(w, r, acl, authz.CapUpdate)
	}
	pols := p.Policies
	if len(pols) == 0 && p.Role != "" {
		pols = []string{string(p.Role)}
	}
	vars := s.authzVars()
	if s.Authz.Allow(pols, acl, authz.CapUpdate, vars) || s.Authz.Allow(pols, acl, authz.CapCreate, vars) {
		return true
	}
	s.deny(w, r, fmt.Sprintf("missing create/update on %s", acl))
	return false
}
