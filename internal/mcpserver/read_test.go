package mcpserver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This package had no tests at all, and it is the surface that hands files to a
// language model. The reader compared path strings and stopped there, which a
// symlink walks straight past.
//
// It is not a hypothetical for a client space: its contents arrive from a
// server, and `contextd pull` writes the paths the server names. A hostile or
// compromised server plants a link, the model reads it, and the contents leave
// the machine inside a prompt.

func spaceWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestReadingAnOrdinaryFileWorks(t *testing.T) {
	root := spaceWith(t, map[string]string{"team/principles.md": "how we work"})

	got, err := readSpaceFile(root, "team/principles.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "how we work" {
		t.Errorf("got %q", got)
	}
}

func TestASymlinkOutOfTheSpaceIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := spaceWith(t, map[string]string{"team/real.md": "fine"})
	link := filepath.Join(root, "team", "notes.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}

	got, err := readSpaceFile(root, "team/notes.md")
	if err == nil {
		t.Fatalf("read through a symlink out of the space and returned %q", got)
	}
	if strings.Contains(got, "PRIVATE KEY") {
		t.Fatal("the secret was returned alongside the error")
	}
}

// A directory symlink is the same escape one level up.
func TestASymlinkedDirectoryIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := spaceWith(t, nil)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	if got, err := readSpaceFile(root, "escape/secret.md"); err == nil {
		t.Fatalf("read through a symlinked directory and returned %q", got)
	}
}

// A link that stays inside the space is legitimate and must keep working.
func TestASymlinkInsideTheSpaceIsAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	root := spaceWith(t, map[string]string{"team/real.md": "inside"})
	if err := os.Symlink(filepath.Join(root, "team", "real.md"), filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}

	got, err := readSpaceFile(root, "alias.md")
	if err != nil {
		t.Fatalf("a link within the space was refused: %v", err)
	}
	if got != "inside" {
		t.Errorf("got %q", got)
	}
}

func TestTraversalIsRefused(t *testing.T) {
	root := spaceWith(t, map[string]string{"notes.md": "here"})
	for _, bad := range []string{"../outside.md", "team/../../outside.md", "/etc/passwd", ""} {
		if got, err := readSpaceFile(root, bad); err == nil {
			t.Errorf("%q was accepted and returned %q", bad, got)
		}
	}
}

// The whole body goes into a tool result bound for a model. Nothing in a context
// space is legitimately this big, and refusing is better than sending it.
func TestAnEnormousFileIsRefused(t *testing.T) {
	root := spaceWith(t, map[string]string{"big.md": strings.Repeat("x", maxToolFileBytes+1)})

	if _, err := readSpaceFile(root, "big.md"); err == nil {
		t.Fatal("a file over the limit was read into a tool result")
	}
	if _, err := readSpaceFile(root, "big.md"); err != nil && !strings.Contains(err.Error(), "too large") {
		t.Errorf("the error does not say why: %v", err)
	}
}

func TestADirectoryIsNotAFile(t *testing.T) {
	root := spaceWith(t, map[string]string{"team/principles.md": "x"})
	if _, err := readSpaceFile(root, "team"); err == nil {
		t.Fatal("a directory was read as a file")
	}
}

// The listing must not advertise what the reader will refuse, or the model is
// invited to ask for something that then fails.
func TestListingSkipsLinksOutOfTheSpace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := spaceWith(t, map[string]string{"real.md": "inside"})
	if err := os.Symlink(secret, filepath.Join(root, "escape.md")); err != nil {
		t.Fatal(err)
	}

	files, err := listFiles(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "escape.md" {
			t.Error("the listing offers a file that leaves the space")
		}
	}
	if len(files) == 0 || files[0] != "real.md" {
		t.Errorf("the real file went missing from the listing: %v", files)
	}
}
