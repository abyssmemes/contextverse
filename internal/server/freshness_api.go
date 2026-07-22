package server

import (
	"net/http"
	"time"

	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/freshness"
	"github.com/abyssmemes/contextverse/internal/webhooks"
)

func (s *Server) handleFreshness(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("space")
	if !s.requireCap(w, r, "spaces/"+name+"/files", authz.CapList) {
		return
	}
	root := s.Spaces.SpaceRoot(name)
	all, err := freshness.ScanDir(root, time.Now().UTC())
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	listed := all
	if r.URL.Query().Get("stale") == "1" || r.URL.Query().Get("stale") == "true" {
		listed = freshness.StaleOnly(all)
	}
	items := make([]map[string]any, 0, len(listed))
	for _, m := range listed {
		items = append(items, map[string]any{
			"path":           m.Path,
			"last_validated": m.LastValidated.Format("2006-01-02"),
			"stale_after":    m.StaleAfter.String(),
			"owner":          m.Owner,
			"confidence":     m.Confidence,
			"stale":          m.Stale,
			"source":         m.Source,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"space": name,
		"files": items,
		"stale": len(freshness.StaleOnly(all)),
	})
}

func (s *Server) maybeQuotaWarning(r *http.Request, space string) {
	if s.Spaces == nil || s.Dispatch == nil {
		return
	}
	bytes, files, err := s.Spaces.SpaceUsage(r.Context(), space)
	if err != nil {
		return
	}
	quota, used, limit, ok := s.Spaces.Quotas.NearLimit(bytes, files)
	if !ok {
		return
	}
	s.Dispatch.Emit(webhooks.Event{
		Type:  "quota.warning",
		Space: space,
		Actor: actorFrom(r, principalFrom(r.Context())).Username,
		Data:  map[string]any{"quota": quota, "used": used, "limit": limit},
	})
}
