package server

import (
	"net/http"
	"strconv"

	"github.com/abyssmemes/contextverse/internal/audit"
	"github.com/abyssmemes/contextverse/internal/authz"
)

func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/audit", authz.CapList) {
		return
	}
	f, err := auditFilterFromRequest(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_argument", err.Error(), nil)
		return
	}
	if s.Audit == nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}, "count": 0})
		return
	}
	entries, err := s.Audit.Query(f)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "count": len(entries)})
}

func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/audit", authz.CapRead) {
		return
	}
	f, err := auditFilterFromRequest(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_argument", err.Error(), nil)
		return
	}
	f.Limit = -1
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "jsonl"
	}
	if s.Audit == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
		_ = s.Audit.ExportCSV(w, f)
	default:
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit.jsonl"`)
		_ = s.Audit.ExportJSONL(w, f)
	}
}

func (s *Server) handleAuditStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "sys/audit", authz.CapRead) {
		return
	}
	f, err := auditFilterFromRequest(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_argument", err.Error(), nil)
		return
	}
	if s.Audit == nil {
		writeJSON(w, http.StatusOK, audit.Stats{ByAction: map[string]int{}})
		return
	}
	st, err := s.Audit.Stats(f)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func auditFilterFromRequest(r *http.Request) (audit.Filter, error) {
	q := r.URL.Query()
	f := audit.Filter{
		Actor:  q.Get("actor"),
		Action: q.Get("action"),
		Space:  q.Get("space"),
		Result: q.Get("result"),
	}
	if s := q.Get("since"); s != "" {
		ts, err := audit.ParseSince(s)
		if err != nil {
			return f, err
		}
		f.Since = ts
	}
	if lim := q.Get("limit"); lim != "" {
		n, err := strconv.Atoi(lim)
		if err != nil {
			return f, err
		}
		f.Limit = n
	}
	return f, nil
}
