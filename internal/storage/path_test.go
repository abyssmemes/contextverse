package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCleanPath(t *testing.T) {
	ok := map[string]string{
		"":                 "",
		"/":                "",
		".":                "",
		"notes.md":         "notes.md",
		"/notes.md":        "notes.md",
		"team/./notes.md":  "team/notes.md",
		"team/sub/../a.md": "team/a.md",
		`team\notes.md`:    "team/notes.md",
		"projects//x/y.md": "projects/x/y.md",
	}
	for in, want := range ok {
		got, err := CleanPath(in)
		if err != nil {
			t.Fatalf("CleanPath(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("CleanPath(%q) = %q, want %q", in, got, want)
		}
	}

	escapes := []string{
		"..",
		"../secrets.md",
		"team/../../secrets.md",
		"/../etc/passwd",
		`..\secrets.md`,
	}
	for _, in := range escapes {
		if _, err := CleanPath(in); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("CleanPath(%q) must escape-reject, got %v", in, err)
		}
	}

	if _, err := CleanPath("a\x00b"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("NUL byte must be rejected")
	}
	if _, err := CleanFilePath(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("empty object path must be rejected")
	}
}

func TestResolveUnderRefusesSymlinkOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolveUnder(root, "escape/loot.md"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("symlinked directory must be refused, got %v", err)
	}
	abs, err := ResolveUnder(root, "team/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "team", "notes.md"); abs != want {
		t.Fatalf("resolved %q, want %q", abs, want)
	}
}

func TestValidSpaceName(t *testing.T) {
	for _, name := range []string{"team", "solo-1", "a_b.c"} {
		if err := ValidSpaceName(name); err != nil {
			t.Fatalf("%q should be valid: %v", name, err)
		}
	}
	for _, name := range []string{"", "..", ".", ".hidden", "a/b", "a b", "тим", "x:y"} {
		if err := ValidSpaceName(name); err == nil {
			t.Fatalf("%q should be rejected", name)
		}
	}
}

func TestLocalRefusesTraversal(t *testing.T) {
	ctx := context.Background()
	l, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "../escape.md", []byte("x"), ""); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("put outside the store must be refused, got %v", err)
	}
	if _, _, err := l.Get(ctx, "../escape.md"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("get outside the store must be refused, got %v", err)
	}
}

func TestGitRefusesTraversal(t *testing.T) {
	ctx := context.Background()
	g, err := OpenGit(GitConfig{LocalPath: filepath.Join(t.TempDir(), "store")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Put(ctx, "../../escape.md", []byte("x"), ""); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("put outside the working tree must be refused, got %v", err)
	}
}

// A shared backend keyed by "<prefix>/<path>" is where traversal turns into a
// cross-space read: "../b/file.md" from space a must not resolve into space b.
func TestPrefixedRefusesCrossSpacePath(t *testing.T) {
	ctx := context.Background()
	inner := newMem()
	a := &Prefixed{Inner: inner, Prefix: "spaces/a"}
	b := &Prefixed{Inner: inner, Prefix: "spaces/b"}

	if _, err := b.Put(ctx, "secret.md", []byte("B"), ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Get(ctx, "../b/secret.md"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("cross-space read must be refused, got %v", err)
	}
	if _, err := a.Put(ctx, "../b/secret.md", []byte("owned"), "v1"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("cross-space write must be refused, got %v", err)
	}
	data, _, err := b.Get(ctx, "secret.md")
	if err != nil || string(data) != "B" {
		t.Fatalf("neighbour object changed: %q %v", data, err)
	}
}
