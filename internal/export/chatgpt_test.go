package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatGPT(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "export")
	mustWrite(t, filepath.Join(root, "context-entry.md"), "# entry\n")
	mustWrite(t, filepath.Join(root, "identity", "me.md"), "# me\n")
	mustWrite(t, filepath.Join(root, "team", "principles.md"), "# rules\n")

	res, err := ChatGPT(root, out, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) < 4 {
		t.Fatalf("written=%v", res.Written)
	}
	raw, err := os.ReadFile(filepath.Join(out, "01-context-entry.md"))
	if err != nil || string(raw) != "# entry\n" {
		t.Fatalf("%q %v", raw, err)
	}
	readme, err := os.ReadFile(filepath.Join(out, "README.md"))
	if err != nil || !strings.Contains(string(readme), "System prompt") {
		t.Fatalf("readme: %s", readme)
	}
	if len(res.Missing) == 0 {
		t.Fatal("expected missing space-index/decisions")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
