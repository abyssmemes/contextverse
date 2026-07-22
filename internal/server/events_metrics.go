package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/events"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/webhooks"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.Metrics == nil {
		http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_ = s.Metrics.WritePrometheus(w)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, "spaces/", authz.CapList) {
		return
	}
	if s.Events == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "unavailable", "events hub not ready", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, r, http.StatusInternalServerError, "internal", "streaming unsupported", nil)
		return
	}
	scopes := events.ParseScopes(r.URL.Query().Get("scopes"))
	ch := s.Events.Subscribe(scopes)
	if s.Metrics != nil {
		s.Metrics.SSEClients.Store(int64(s.Events.Clients()))
	}
	defer func() {
		s.Events.Unsubscribe(ch)
		if s.Metrics != nil {
			s.Metrics.SSEClients.Store(int64(s.Events.Clients()))
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	bw := bufio.NewWriter(w)
	writeEvt := func(evt webhooks.Event) error {
		payload, err := json.Marshal(evt)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(bw, "id: %s\nevent: %s\ndata: %s\n\n", evt.ID, evt.Type, payload); err != nil {
			return err
		}
		return bw.Flush()
	}

	if last := r.Header.Get("Last-Event-ID"); last != "" {
		for _, evt := range s.Events.ReplaySince(last, scopes) {
			if err := writeEvt(evt); err != nil {
				return
			}
		}
	}

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := bw.WriteString(": ping\n\n"); err != nil {
				return
			}
			if err := bw.Flush(); err != nil {
				return
			}
			flusher.Flush()
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if err := writeEvt(evt); err != nil {
				logx.L().Debug("sse write", "err", err)
				return
			}
			flusher.Flush()
		}
	}
}
