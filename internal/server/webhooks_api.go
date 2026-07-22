package server

import (
	"encoding/json"
	"net/http"

	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/webhooks"
)

func (s *Server) handleWebhooksList(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/webhooks", authz.CapList) {
		return
	}
	if s.Hooks == nil {
		writeJSON(w, http.StatusOK, map[string]any{"webhooks": []any{}})
		return
	}
	list, err := s.Hooks.List()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	out := make([]webhooks.Hook, 0, len(list))
	for _, h := range list {
		out = append(out, h.Public())
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": out})
}

func (s *Server) handleWebhooksGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/webhooks", authz.CapRead) {
		return
	}
	id := r.PathValue("id")
	h, ok, err := s.Hooks.Get(id)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	if !ok {
		writeErr(w, r, http.StatusNotFound, "not_found", "webhook not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, h.Public())
}

func (s *Server) handleWebhooksCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/webhooks", authz.CapCreate) {
		return
	}
	var body struct {
		URL     string   `json:"url"`
		Events  []string `json:"events"`
		Space   string   `json:"space"`
		Secret  string   `json:"secret"`
		Enabled *bool    `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", "invalid json", nil)
		return
	}
	h := webhooks.Hook{
		URL:     body.URL,
		Events:  body.Events,
		Space:   body.Space,
		Secret:  body.Secret,
		Enabled: true,
	}
	if body.Enabled != nil {
		h.Enabled = *body.Enabled
	}
	saved, err := s.Hooks.Upsert(h)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	s.auditEmit(r, "webhook.create", body.Space, saved.ID, nil)
	// return secret once
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":      saved.ID,
		"url":     saved.URL,
		"events":  saved.Events,
		"space":   saved.Space,
		"enabled": saved.Enabled,
		"secret":  saved.Secret,
	})
}

func (s *Server) handleWebhooksDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/webhooks", authz.CapDelete) {
		return
	}
	id := r.PathValue("id")
	if err := s.Hooks.Delete(id); err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	s.auditEmit(r, "webhook.delete", "", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWebhooksTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/webhooks", authz.CapUpdate) {
		return
	}
	id := r.PathValue("id")
	if s.Dispatch == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "unavailable", "webhooks not initialized", nil)
		return
	}
	if err := s.Dispatch.Test(r.Context(), id); err != nil {
		writeErr(w, r, http.StatusBadGateway, "delivery_failed", err.Error(), nil)
		return
	}
	s.auditEmit(r, "webhook.test", "", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleWebhooksDeadLetter(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/webhooks", authz.CapList) {
		return
	}
	list, err := s.Hooks.ListDeadLetter(100)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list, "count": len(list)})
}
