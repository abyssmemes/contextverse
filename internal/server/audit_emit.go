package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/abyssmemes/contextverse/internal/audit"
	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/webhooks"
)

// auditEmit appends a successful mutation (best-effort; never fails the request).
func (s *Server) auditEmit(r *http.Request, action, space, target string, diff *audit.Diff) {
	s.auditWrite(r, action, space, target, audit.ResultSuccess, "", diff)
}

func (s *Server) auditDenied(r *http.Request, action, space, target, msg string) {
	s.auditWrite(r, action, space, target, audit.ResultDenied, msg, nil)
}

func (s *Server) auditError(r *http.Request, action, space, target string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	s.auditWrite(r, action, space, target, audit.ResultError, msg, nil)
}

func (s *Server) auditWrite(r *http.Request, action, space, target, result, errMsg string, diff *audit.Diff) {
	if s.Audit == nil {
		return
	}
	p := principalFrom(r.Context())
	if p == nil {
		p = s.principalFromRequest(r)
	}
	e := audit.Entry{
		Action: action,
		Space:  space,
		Target: target,
		Result: result,
		Error:  errMsg,
		Diff:   diff,
		Actor:  actorFrom(r, p),
	}
	if err := s.Audit.Append(e); err != nil {
		logx.L().Warn("audit append", "err", err, "action", action)
	}
	if result == audit.ResultSuccess {
		s.emitWebhook(r, action, space, target, diff)
	}
}

func (s *Server) emitWebhook(r *http.Request, action, space, target string, diff *audit.Diff) {
	if s.Dispatch == nil {
		return
	}
	p := principalFrom(r.Context())
	actor := ""
	if p != nil {
		actor = p.User
	}
	data := map[string]any{}
	if target != "" {
		data["target"] = target
	}
	if diff != nil {
		data["ops"] = diff.Ops
	}
	s.Dispatch.Emit(webhooks.Event{
		Type:  action,
		Space: space,
		Scope: target,
		Actor: actor,
		Data:  data,
	})
}

func actorFrom(r *http.Request, p *auth.Principal) audit.Actor {
	a := audit.Actor{Method: "token"}
	if p != nil {
		a.Username = p.User
		a.Role = string(p.Role)
		if a.Role == "" && len(p.Policies) > 0 {
			a.Role = p.Policies[0]
		}
	}
	if r != nil {
		a.IP = clientIP(r)
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			a.Method = "session"
		}
	}
	return a
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
