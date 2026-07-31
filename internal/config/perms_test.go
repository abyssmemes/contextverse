package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// These files hold git_token, s3_secret_key and sql_dsn — a DSN with the
// password inline — and were written world-readable while the bearer token
// beside them was already owner-only. On a shared host or in a container with a
// second user, that is a credential given away.

func TestSavedConfigIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	root := t.TempDir()
	cfg := &Config{
		Mode:      ModeSolo,
		SpaceRoot: root,
		Backend:   Backend{Driver: "s3", S3SecretKey: "not-for-everyone"},
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, Path(root))
}

func TestSavedServerConfigIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	dir := t.TempDir()
	cfg := &ServerConfig{
		Mode:    ModeServer,
		DataDir: dir,
		Backend: Backend{Driver: "sql", SQLDSN: "postgres://u:secret@db/cv"},
	}
	if err := SaveServer(cfg); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, ServerConfigPathIn(dir))
}

// A config written by an earlier version is already on disk and world-readable.
// Nobody is going to notice that on their own, so reading it repairs it.
func TestLoadingRepairsAWorldReadableConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	root := t.TempDir()
	cfg := &Config{Mode: ModeSolo, SpaceRoot: root, Backend: Backend{Driver: "local"}}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	// Put it back the way the old version left it.
	if err := os.Chmod(Path(root), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(root); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, Path(root))
}

func TestLoadingRepairsAWorldReadableServerConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	dir := t.TempDir()
	if err := SaveServer(&ServerConfig{Mode: ModeServer, DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ServerConfigPathIn(dir), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadServer(dir); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, ServerConfigPathIn(dir))
}

// A config left more restrictive than we ask for stays that way.
func TestRepairDoesNotLoosenPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("mode: solo\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := restrictSecretFile(path); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o400 {
		t.Errorf("mode %o, want the stricter 0400 left alone", st.Mode().Perm())
	}
}

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("%s is mode %o; group and world can read credentials", path, perm)
	}
}
