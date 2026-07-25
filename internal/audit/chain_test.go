package audit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeEntries(t *testing.T, l *Logger, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := l.Append(Entry{
			Actor:  Actor{Username: "alice"},
			Action: "space.write",
			Space:  "team",
			Target: "notes.md",
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

func dayFile(t *testing.T, l *Logger) string {
	t.Helper()
	files, err := l.dayFiles()
	if err != nil || len(files) == 0 {
		t.Fatalf("no day files: %v", err)
	}
	return filepath.Join(l.dir, files[0])
}

func TestChainVerifiesCleanLog(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeEntries(t, l, 5)
	n, err := l.Verify()
	if err != nil {
		t.Fatalf("clean log must verify: %v", err)
	}
	if n != 5 {
		t.Fatalf("verified %d entries, want 5", n)
	}
}

func TestEditedRecordIsDetected(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeEntries(t, l, 3)

	path := dayFile(t, l)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var e Entry
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil {
		t.Fatal(err)
	}
	e.Actor.Username = "mallory"
	edited, _ := json.Marshal(e)
	lines[1] = string(edited)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := l.Verify(); err == nil {
		t.Fatal("editing an actor must break the chain")
	} else {
		var br ChainBreak
		if !errors.As(err, &br) {
			t.Fatalf("want ChainBreak, got %T: %v", err, err)
		}
		if br.Line != 2 {
			t.Fatalf("break reported at line %d, want 2", br.Line)
		}
	}
}

func TestDeletedRecordIsDetected(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeEntries(t, l, 4)

	path := dayFile(t, l)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	kept := append(lines[:1:1], lines[2:]...) // drop the second record
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := l.Verify(); err == nil {
		t.Fatal("removing a record must break the chain")
	}
}

func TestChainSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeEntries(t, first, 2)

	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeEntries(t, second, 2)

	if _, err := second.Verify(); err != nil {
		t.Fatalf("a restarted logger must continue the chain: %v", err)
	}
	if second.head == "" {
		t.Fatal("head was not picked up on open")
	}
}

func TestChainSpansDayFiles(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	if err := l.Append(Entry{Timestamp: yesterday, Actor: Actor{Username: "alice"}, Action: "auth.login"}); err != nil {
		t.Fatal(err)
	}
	writeEntries(t, l, 1)

	files, _ := l.dayFiles()
	if len(files) != 2 {
		t.Fatalf("expected two day files, got %v", files)
	}
	if _, err := l.Verify(); err != nil {
		t.Fatalf("chain must span rotation: %v", err)
	}

	// Dropping the whole earlier day must not go unnoticed.
	if err := os.Remove(filepath.Join(dir, "audit", files[0])); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Verify(); err == nil {
		t.Fatal("deleting a day file must break the chain")
	}
}

func TestPreChainEntriesAreTolerated(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	// Simulate a log written by an older build: no prev, no hash.
	legacy, _ := json.Marshal(Entry{
		ID:        "evt_legacy",
		Timestamp: time.Now().UTC(),
		Actor:     Actor{Username: "alice"},
		Action:    "space.read",
		Result:    ResultSuccess,
	})
	path := filepath.Join(dir, "audit", time.Now().UTC().Format("2006-01-02")+".jsonl")
	if err := os.WriteFile(path, append(legacy, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeEntries(t, reopened, 2)
	n, err := reopened.Verify()
	if err != nil {
		t.Fatalf("legacy lines must not fail verification: %v", err)
	}
	if n != 2 {
		t.Fatalf("verified %d, want the 2 chained entries", n)
	}
}

func TestAuditFilesAreNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeEntries(t, l, 1)
	fi, err := os.Stat(dayFile(t, l))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("audit file mode %v exposes the log to other users", fi.Mode().Perm())
	}
}
