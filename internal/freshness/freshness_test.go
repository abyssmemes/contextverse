package freshness

import (
	"testing"
	"time"
)

func TestParseAndStale(t *testing.T) {
	raw := []byte("---\nlast-validated: 2026-01-01\nstale-after: 30d\nowner: bob\n---\n\n# Hi\n")
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	m, ok := Parse("team/x.md", raw, now)
	if !ok || m.Owner != "bob" || m.StaleAfter != 30*24*time.Hour {
		t.Fatalf("%+v ok=%v", m, ok)
	}
	m.Stale = now.After(m.LastValidated.Add(m.StaleAfter))
	if !m.Stale {
		t.Fatalf("expected stale: validated=%v after=%v", m.LastValidated, m.StaleAfter)
	}
}

func TestStampValidated(t *testing.T) {
	raw := []byte("---\nstale-after: 7d\n---\n\nbody\n")
	out, err := StampValidated(raw, time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := Parse("a.md", out, time.Now())
	if !ok || m.LastValidated.Format("2006-01-02") != "2026-07-22" {
		t.Fatalf("%+v %s", m, out)
	}
}
