package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/orkcom-tech/contextverse/internal/audit"
)

// seedAudit writes a log with a deliberately tied count, so any ordering that
// depends on map iteration shows up as different output between runs.
func seedAudit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	lg, err := audit.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	entries := []struct {
		action, actor, result string
	}{
		{"file.write", "ann", audit.ResultSuccess},
		{"file.write", "ann", audit.ResultSuccess},
		{"file.read", "bob", audit.ResultSuccess},
		{"file.read", "bob", audit.ResultSuccess},
		{"space.create", "ann", audit.ResultDenied},
	}
	for i, e := range entries {
		if err := lg.Append(audit.Entry{
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Actor:     audit.Actor{Username: e.actor},
			Action:    e.action,
			Space:     "main",
			Target:    "some/very/long/path/that/the/table/will/shorten/for/display.md",
			Result:    e.result,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Two identical invocations printed different output, because the summary
// ranged a map. A command you cannot diff against itself is a command you
// cannot use in a check.
func TestAuditStatsIsStableAcrossRuns(t *testing.T) {
	dir := seedAudit(t)

	first, err := run(t, "--server-dir", dir, "audit", "stats")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		again, err := run(t, "--server-dir", dir, "audit", "stats")
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("run %d differs from run 1:\n--- first ---\n%s\n--- again ---\n%s", i+2, first, again)
		}
	}
}

// Ties are the case that exposes an unstable sort: file.write and file.read
// both occur twice, so only a tie-breaker keeps their order fixed.
func TestAuditStatsOrdersByCountThenName(t *testing.T) {
	dir := seedAudit(t)

	raw, err := run(t, "--server-dir", dir, "audit", "stats", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got AuditStats
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, raw)
	}
	want := []AuditActionCount{
		{"file.read", 2},
		{"file.write", 2},
		{"space.create", 1},
	}
	if len(got.ByAction) != len(want) {
		t.Fatalf("got %d actions, want %d: %+v", len(got.ByAction), len(want), got.ByAction)
	}
	for i := range want {
		if got.ByAction[i] != want[i] {
			t.Errorf("position %d: got %+v, want %+v", i, got.ByAction[i], want[i])
		}
	}
	if got.Failed != 1 {
		t.Errorf("Failed = %d, want 1", got.Failed)
	}
}

// The table shortens long targets so it stays readable. A structured payload
// must not: a truncated path is a path a script cannot open.
func TestAuditJSONKeepsFullTargets(t *testing.T) {
	dir := seedAudit(t)
	const full = "some/very/long/path/that/the/table/will/shorten/for/display.md"

	raw, err := run(t, "--server-dir", dir, "audit", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []AuditEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, raw)
	}
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	for _, e := range entries {
		if e.Target != full {
			t.Errorf("target came back as %q; a truncated path cannot be opened", e.Target)
			break
		}
	}
}
