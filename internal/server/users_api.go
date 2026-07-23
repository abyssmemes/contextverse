package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/authz"
)

func (s *Server) registerUsersAPI(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/users", s.auth(s.handleListUsers))
	mux.Handle("POST /api/v1/users", s.auth(s.handleCreateUser))
	mux.Handle("POST /api/v1/users/{name}/tokens", s.auth(s.handleCreateUserToken))
	mux.Handle("PUT /api/v1/policies/{name}", s.auth(s.handlePutPolicy))
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/auth/users", authz.CapList) {
		return
	}
	users, err := s.Auth.ListUsers()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"name":     u.Name,
			"role":     u.Role,
			"policies": u.EffectivePolicies(),
			"disabled": u.Disabled,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/auth/users", authz.CapCreate) {
		return
	}
	var body struct {
		Name       string   `json:"name"`
		Role       string   `json:"role"`
		Policies   []string `json:"policies"`
		IssueToken bool     `json:"issue_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "invalid json", nil)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "name required", nil)
		return
	}
	role := auth.Role(strings.TrimSpace(body.Role))
	if role == "" {
		role = auth.RoleContributor
	}
	if err := s.Auth.AddUser(name, role); err != nil {
		writeErr(w, r, http.StatusConflict, "conflict", err.Error(), nil)
		return
	}
	if len(body.Policies) > 0 {
		if err := s.Auth.SetPolicies(name, body.Policies); err != nil {
			writeErr(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
	}
	resp := map[string]any{"name": name, "role": role, "policies": body.Policies}
	if body.IssueToken {
		tok, _, err := s.Auth.CreateToken(name, "api")
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
			return
		}
		resp["token"] = tok
	}
	s.auditEmit(r, "user.add", "", name+":"+string(role), nil)
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleCreateUserToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/auth/users", authz.CapUpdate) {
		return
	}
	name := r.PathValue("name")
	var body struct {
		Label string `json:"label"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Label == "" {
		body.Label = "api"
	}
	tok, rec, err := s.Auth.CreateToken(name, body.Label)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	s.auditEmit(r, "token.create", "", name+":"+rec.ID, nil)
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": tok,
		"id":    rec.ID,
		"user":  name,
		"label": rec.Label,
	})
}

func (s *Server) handlePutPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/auth/policies", authz.CapUpdate) {
		return
	}
	name := r.PathValue("name")
	var body struct {
		Description string         `json:"description"`
		Rules       []authz.Rule   `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "invalid json", nil)
		return
	}
	if len(body.Rules) == 0 {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "rules required", nil)
		return
	}
	p := authz.Policy{
		Name:        name,
		Description: body.Description,
		Rules:       body.Rules,
	}
	if err := s.Authz.Write(p); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	s.auditEmit(r, "policy.write", "", name, nil)
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "rules": len(body.Rules)})
}
