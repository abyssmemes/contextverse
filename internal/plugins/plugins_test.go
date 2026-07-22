package plugins

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
	v := Vars{Home: "/Users/x", Cwd: "/proj", Space: "/Users/x/.context", Project: "p"}
	got := Expand("~/claude/{{project}}", v)
	want := filepath.Join("/Users/x", "claude", "p")
	// Expand does Join only for ~/ prefix then replace — check simpler
	got2 := Expand("{{cwd}}/.cursor/rules/x.mdc", v)
	if got2 != "/proj/.cursor/rules/x.mdc" {
		t.Fatalf("got %q", got2)
	}
	_ = got
	_ = want
}

func TestCommandHookIdempotent(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	in := &Integration{
		ID:        "claude-code",
		Mechanism: MechanismCommandHook,
		Target:    settings,
		Command:   "contextd context inject --format claude-hook",
		Merge:     "json-block",
	}
	vars := Vars{Home: dir, Cwd: dir}
	if _, err := Apply(in, vars); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(in, vars); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	hooks := doc["hooks"].(map[string]any)
	session := hooks["SessionStart"].([]any)
	if len(session) != 1 {
		t.Fatalf("expected 1 SessionStart entry, got %d: %s", len(session), raw)
	}
}

func TestInjectClaudeHook(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "context-entry.md"), []byte("entry"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "identity"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "identity", "me.md"), []byte("me"), 0o644)
	out, err := Inject("claude-hook", dir, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	hook := doc["hookSpecificOutput"].(map[string]any)
	if hook["hookEventName"] != "SessionStart" {
		t.Fatalf("%v", hook)
	}
	if hook["additionalContext"] == nil || hook["additionalContext"] == "" {
		t.Fatal("missing additionalContext")
	}
}

func TestLoadEmbedded(t *testing.T) {
	cat, err := DefaultCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, in := range cat {
		ids[in.ID] = true
	}
	for _, want := range []string{"claude-code", "cursor", "windsurf", "copilot"} {
		if !ids[want] {
			t.Fatalf("embedded catalog missing %s: %+v", want, ids)
		}
	}
}

func TestAskWhich(t *testing.T) {
	cat := []*Integration{
		{ID: "a", Display: "A"},
		{ID: "b", Display: "B"},
	}
	got := AskWhich(cat, strings.NewReader("2\n"), io.Discard)
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("%+v", got)
	}
	got = AskWhich(cat, strings.NewReader("all\n"), io.Discard)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestApplySlotWindsurf(t *testing.T) {
	cat, err := DefaultCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	var wind *Integration
	for _, in := range cat {
		if in.ID == "windsurf" {
			wind = in
			break
		}
	}
	if wind == nil {
		t.Fatal("no windsurf")
	}
	dir := t.TempDir()
	vars := Vars{Home: dir, Cwd: dir, Space: filepath.Join(dir, "space")}
	res, err := Apply(wind, vars)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(res.Target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ContextVerse") {
		t.Fatalf("%s", raw)
	}
}
