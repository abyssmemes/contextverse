package audit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendQueryExport(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Entry{
		Actor:  Actor{Username: "alice", Role: "contributor", Method: "token"},
		Action: "space.push",
		Space:  "team",
		Target: "docs/a.md",
		Result: ResultSuccess,
		Diff:   &Diff{Ops: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Entry{
		Actor:  Actor{Username: "bob", Role: "viewer"},
		Action: "file.write",
		Space:  "team",
		Result: ResultDenied,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := l.Query(Filter{Actor: "alice", Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("alice: %v %v", got, err)
	}
	if got[0].Action != "space.push" || got[0].ID == "" {
		t.Fatalf("%+v", got[0])
	}

	got, err = l.Query(Filter{Action: "*push*", Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("action: %v %v", got, err)
	}

	st, err := l.Stats(Filter{})
	if err != nil || st.Entries != 2 || st.Failed != 1 || st.Actors != 2 {
		t.Fatalf("stats %+v err=%v", st, err)
	}

	day := filepath.Join(l.Dir(), time.Now().UTC().Format("2006-01-02")+".jsonl")
	if _, err := os.Stat(day); err != nil {
		t.Fatal(err)
	}
}

func TestParseSince(t *testing.T) {
	ts, err := ParseSince("24h")
	if err != nil || time.Since(ts) < 23*time.Hour {
		t.Fatalf("%v %v", ts, err)
	}
	ts, err = ParseSince("7d")
	if err != nil || time.Since(ts) < 6*24*time.Hour {
		t.Fatalf("%v %v", ts, err)
	}
}
