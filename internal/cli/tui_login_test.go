package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertAndRemoveMarkedBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".zprofile")
	if err := os.WriteFile(path, []byte("# existing\nexport FOO=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	block := tuiLoginBegin + "\n# body\n" + tuiLoginEnd + "\n"
	if err := upsertMarkedBlock(path, tuiLoginBegin, tuiLoginEnd, block); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "export FOO=1") {
		t.Fatalf("lost existing content:\n%s", body)
	}
	if !strings.Contains(body, tuiLoginBegin) || !strings.Contains(body, tuiLoginEnd) {
		t.Fatalf("markers missing:\n%s", body)
	}
	// replace with new block
	block2 := tuiLoginBegin + "\n# replaced\n" + tuiLoginEnd + "\n"
	if err := upsertMarkedBlock(path, tuiLoginBegin, tuiLoginEnd, block2); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	body = string(raw)
	if strings.Count(body, tuiLoginBegin) != 1 {
		t.Fatalf("expected one begin marker, got:\n%s", body)
	}
	if !strings.Contains(body, "# replaced") {
		t.Fatalf("expected replaced body:\n%s", body)
	}
	ok, err := removeMarkedBlock(path, tuiLoginBegin, tuiLoginEnd)
	if err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	raw, _ = os.ReadFile(path)
	body = string(raw)
	if strings.Contains(body, tuiLoginBegin) {
		t.Fatalf("markers still present:\n%s", body)
	}
	if !strings.Contains(body, "export FOO=1") {
		t.Fatalf("lost existing after remove:\n%s", body)
	}
}
