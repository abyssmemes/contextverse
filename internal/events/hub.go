package events

import (
	"strings"
	"sync"
	"time"

	"github.com/abyssmemes/contextverse/internal/webhooks"
)

const ringSize = 256

type sub struct {
	ch     chan webhooks.Event
	scopes []string
}

// Hub fans events to SSE subscribers with a small replay ring.
type Hub struct {
	mu   sync.Mutex
	subs map[chan webhooks.Event]*sub
	ring []webhooks.Event
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[chan webhooks.Event]*sub{}}
}

// Publish stores and fans out an event.
func (h *Hub) Publish(evt webhooks.Event) {
	if h == nil {
		return
	}
	if evt.Created.IsZero() {
		evt.Created = time.Now().UTC()
	}
	h.mu.Lock()
	h.ring = append(h.ring, evt)
	if len(h.ring) > ringSize {
		h.ring = h.ring[len(h.ring)-ringSize:]
	}
	subs := make([]*sub, 0, len(h.subs))
	for _, s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()

	for _, s := range subs {
		if !MatchScopes(s.scopes, evt) {
			continue
		}
		select {
		case s.ch <- evt:
		default:
			// slow consumer — drop
		}
	}
}

// Subscribe registers a buffered channel. Caller must Unsubscribe.
func (h *Hub) Subscribe(scopes []string) chan webhooks.Event {
	ch := make(chan webhooks.Event, 32)
	h.mu.Lock()
	h.subs[ch] = &sub{ch: ch, scopes: scopes}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes the channel.
func (h *Hub) Unsubscribe(ch chan webhooks.Event) {
	if h == nil || ch == nil {
		return
	}
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Clients returns current subscriber count.
func (h *Hub) Clients() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// ReplaySince returns ring events after lastID (exclusive), filtered by scopes.
func (h *Hub) ReplaySince(lastID string, scopes []string) []webhooks.Event {
	if h == nil || lastID == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []webhooks.Event
	seen := false
	for _, e := range h.ring {
		if !seen {
			if e.ID == lastID {
				seen = true
			}
			continue
		}
		if MatchScopes(scopes, e) {
			out = append(out, e)
		}
	}
	return out
}

// MatchScopes reports whether evt matches any scope prefix (empty = all).
func MatchScopes(scopes []string, evt webhooks.Event) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" || s == "*" {
			return true
		}
		pref := strings.TrimSuffix(s, "/")
		if evt.Space != "" && (evt.Space == pref || strings.HasPrefix(evt.Space, pref+"/")) {
			return true
		}
		if pref == evt.Space {
			return true
		}
		if evt.Scope != "" {
			if evt.Scope == pref || strings.HasPrefix(evt.Scope, pref+"/") || strings.HasPrefix(evt.Scope, s) {
				return true
			}
		}
		// filter "team/" matches space "team"
		if evt.Space != "" && (s == evt.Space+"/" || strings.HasPrefix(s, evt.Space+"/")) {
			return true
		}
	}
	return false
}

// ParseScopes splits ?scopes=a,b.
func ParseScopes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
