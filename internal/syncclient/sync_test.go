package syncclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/spacesvc"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func alwaysSync() spacesvc.SyncConfig {
	return spacesvc.SyncConfig{Default: "always"}
}

func opsByPath(ops []spacesvc.PushOp) map[string]string {
	out := map[string]string{}
	for _, o := range ops {
		out[o.Path] = o.Op
	}
	return out
}

// A push used to walk the tree and put every file every time, so publishing an
// edit to one document re-uploaded the whole space.
func TestOnlyChangedFilesArePushed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.md", "one")
	writeFile(t, root, "b.md", "two")

	// First push from a machine that has sent nothing: everything travels.
	ops, sent, err := collectPushOps(root, alwaysSync(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("first push carried %d ops, want 2", len(ops))
	}

	// Nothing has changed since.
	state := &LocalState{Sent: sent}
	ops, _, err = collectPushOps(root, alwaysSync(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("an unchanged space produced %d ops: %+v", len(ops), ops)
	}

	// One edit, one op.
	writeFile(t, root, "b.md", "two, revised")
	ops, _, err = collectPushOps(root, alwaysSync(), state)
	if err != nil {
		t.Fatal(err)
	}
	if got := opsByPath(ops); len(got) != 1 || got["b.md"] != "put" {
		t.Errorf("got %+v, want only a put for b.md", got)
	}
}

// The one that made people distrust the tool: a file deleted here came back on
// the next pull, because a push only ever said "put".
func TestADeletedFileIsPushedAsADeletion(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "keep.md", "still here")
	writeFile(t, root, "gone.md", "not for long")

	_, sent, err := collectPushOps(root, alwaysSync(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "gone.md")); err != nil {
		t.Fatal(err)
	}

	ops, nextSent, err := collectPushOps(root, alwaysSync(), &LocalState{Sent: sent})
	if err != nil {
		t.Fatal(err)
	}
	got := opsByPath(ops)
	if got["gone.md"] != "delete" {
		t.Errorf("got %+v, want a delete for gone.md", got)
	}
	if _, ok := got["keep.md"]; ok {
		t.Errorf("an unchanged file was pushed as well: %+v", got)
	}
	if _, ok := nextSent["gone.md"]; ok {
		t.Error("the record still lists a file that is gone")
	}
}

// A file that never travelled cannot be deleted remotely by disappearing here.
func TestAFileThatWasNeverSentIsNotDeleted(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "local-only.md", "mine")

	sync := spacesvc.SyncConfig{
		Default: "always",
		Rules:   []spacesvc.SyncRule{{Path: "local-only.md", Mode: "never"}},
	}
	ops, sent, err := collectPushOps(root, sync, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("a never-sync file was pushed: %+v", ops)
	}
	if err := os.Remove(filepath.Join(root, "local-only.md")); err != nil {
		t.Fatal(err)
	}
	ops, _, err = collectPushOps(root, sync, &LocalState{Sent: sent})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("removing a never-sync file produced %+v", ops)
	}
}

// The record is only written once the server has taken the batch. Writing it
// earlier would make the next push skip the files that never arrived.
func TestTheSentRecordIsOnlyKeptAfterTheServerAcceptsIt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.md", "one")

	refuses := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"nope"}}`))
	}))
	defer refuses.Close()

	c := &Client{BaseURL: refuses.URL, Space: "team", HTTP: refuses.Client()}
	st := &LocalState{Sent: map[string]string{}}
	if _, err := c.Push(context.Background(), root, "head", alwaysSync(), st, false); err == nil {
		t.Fatal("a refused push reported success")
	}
	if len(st.Sent) != 0 {
		t.Errorf("a failed push recorded %v as sent", st.Sent)
	}
}

func TestASuccessfulPushRecordsWhatItSent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.md", "one")

	accepts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"head": "h2", "applied": 1})
	}))
	defer accepts.Close()

	c := &Client{BaseURL: accepts.URL, Space: "team", HTTP: accepts.Client()}
	st := &LocalState{Sent: map[string]string{}}
	if _, err := c.Push(context.Background(), root, "h1", alwaysSync(), st, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Sent["a.md"]; !ok {
		t.Errorf("an accepted push recorded %v", st.Sent)
	}
}

// An empty batch must not be sent: it would move the head for no reason and
// make every other client re-check a space that did not change.
func TestNothingChangedSendsNothing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.md", "one")
	_, sent, err := collectPushOps(root, alwaysSync(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_ = json.NewEncoder(w).Encode(map[string]any{"head": "h2", "applied": 0})
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL, Space: "team", HTTP: server.Client()}
	res, err := c.Push(context.Background(), root, "h1", alwaysSync(), &LocalState{Sent: sent}, false)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("an empty push was sent to the server")
	}
	if res.Head != "h1" {
		t.Errorf("head moved to %q on an empty push", res.Head)
	}
}

// ResolveMode decides what leaves the machine. Longest match wins and an exact
// path beats a prefix, which is what every ignore-file people have used does —
// and is not what the old two-pass version did, where the answer depended on the
// order the rules were listed in.
func TestResolveModeTakesTheLongestMatch(t *testing.T) {
	rules := []spacesvc.SyncRule{
		{Path: "identity/", Mode: "init-only"},
		{Path: "identity/shared.md", Mode: "always"},
		{Path: "team/", Mode: "always"},
		{Path: "team/secrets/", Mode: "never"},
	}
	sync := spacesvc.SyncConfig{Default: "always", Rules: rules}

	for path, want := range map[string]string{
		"identity/me.md":            "init-only",
		"identity/shared.md":        "always",
		"team/principles.md":        "always",
		"team/secrets/keys.md":      "never",
		"projects/example/notes.md": "always", // falls through to the default
	} {
		if got := ResolveMode(sync, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

// The same rules in a different order must give the same answers. They did not.
func TestResolveModeDoesNotDependOnRuleOrder(t *testing.T) {
	rules := []spacesvc.SyncRule{
		{Path: "identity/", Mode: "init-only"},
		{Path: "identity/shared.md", Mode: "always"},
		{Path: "team/", Mode: "always"},
		{Path: "team/secrets/", Mode: "never"},
	}
	reversed := make([]spacesvc.SyncRule, len(rules))
	for i, r := range rules {
		reversed[len(rules)-1-i] = r
	}

	forward := spacesvc.SyncConfig{Default: "always", Rules: rules}
	backward := spacesvc.SyncConfig{Default: "always", Rules: reversed}

	for _, path := range []string{
		"identity/me.md",
		"identity/shared.md",
		"team/principles.md",
		"team/secrets/keys.md",
		"stray.md",
	} {
		a, b := ResolveMode(forward, path), ResolveMode(backward, path)
		if a != b {
			t.Errorf("%s: %q listed one way, %q the other", path, a, b)
		}
	}
}

func TestResolveModeFallsBackToTheDefault(t *testing.T) {
	if got := ResolveMode(spacesvc.SyncConfig{}, "anything.md"); got != "always" {
		t.Errorf("an empty config gave %q, want always", got)
	}
	sync := spacesvc.SyncConfig{Default: "never"}
	if got := ResolveMode(sync, "anything.md"); got != "never" {
		t.Errorf("got %q, want the configured default", got)
	}
}

// An exact rule and a prefix rule of the same length: the exact one is the more
// specific statement and must win regardless of which was listed first.
func TestAnExactRuleBeatsAPrefixOfTheSameLength(t *testing.T) {
	sync := spacesvc.SyncConfig{
		Default: "always",
		Rules: []spacesvc.SyncRule{
			{Path: "notes/", Mode: "never"},
			{Path: "notes1", Mode: "always"}, // same length, exact
		},
	}
	if got := ResolveMode(sync, "notes1"); got != "always" {
		t.Errorf("notes1 = %q, want always", got)
	}
	if got := ResolveMode(sync, "notes/x.md"); got != "never" {
		t.Errorf("notes/x.md = %q, want never", got)
	}
}

// The listing the wizard offers. Decoded into the wrong shape, this failed on
// every call and the wizard blamed the server for it.
func TestListSpacesReadsTheServersShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"spaces":[{"name":"team","head":"h1"},{"name":"archive"}]}`))
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL, HTTP: server.Client()}
	spaces, err := c.ListSpaces(context.Background())
	if err != nil {
		t.Fatalf("the listing the server actually sends was refused: %v", err)
	}
	if len(spaces) != 2 || spaces[0].Name != "team" || spaces[0].Head != "h1" || spaces[1].Name != "archive" {
		t.Errorf("decoded %+v", spaces)
	}
}
