package events

import (
	"testing"

	"github.com/abyssmemes/contextverse/internal/webhooks"
)

func TestMatchScopes(t *testing.T) {
	evt := webhooks.Event{Space: "team", Scope: "team/principles.md", Type: "space.push"}
	if !MatchScopes(nil, evt) {
		t.Fatal("nil scopes = all")
	}
	if !MatchScopes([]string{"team"}, evt) {
		t.Fatal("space name")
	}
	if !MatchScopes([]string{"team/"}, evt) {
		t.Fatal("space/")
	}
	if !MatchScopes([]string{"team/principles.md"}, evt) {
		t.Fatal("exact path")
	}
	if MatchScopes([]string{"other"}, evt) {
		t.Fatal("other should miss")
	}
}

func TestHubReplay(t *testing.T) {
	h := NewHub()
	h.Publish(webhooks.Event{ID: "a", Space: "team", Type: "x"})
	h.Publish(webhooks.Event{ID: "b", Space: "team", Type: "y"})
	got := h.ReplaySince("a", nil)
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("%+v", got)
	}
}
