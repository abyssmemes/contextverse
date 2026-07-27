package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A relative space_root used to make Save resolve against whatever directory
// the command was run from. `contextd activate` inside a project therefore wrote
// a stray config.yaml under that project and left the real one untouched —
// which in client mode silently dropped Sync.LastHead, so the client re-pulled
// everything every run and pushed against a stale head.
func TestConfigSavesToItsOwnSpaceFromAnyDirectory(t *testing.T) {
	base := t.TempDir()
	space := filepath.Join(base, "space")
	elsewhere := filepath.Join(base, "project")
	for _, d := range []string{space, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Write a config the way an early version would have: a relative root.
	raw := "mode: client\nspace_root: ./space\n"
	if err := os.WriteFile(filepath.Join(space, ConfigFileName), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// Load from the space's own directory, then move away — the shape of
	// `activate`, which loads the space and then works inside a project.
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("./space")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(cfg.SpaceRoot) {
		t.Fatalf("SpaceRoot = %q, want an absolute path", cfg.SpaceRoot)
	}

	if err := os.Chdir(elsewhere); err != nil {
		t.Fatal(err)
	}
	cfg.Sync.LastHead = "head-after-pull"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	// The real config must have the change...
	reloaded, err := Load(space)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Sync.LastHead != "head-after-pull" {
		t.Errorf("LastHead = %q, want it written to the real config", reloaded.Sync.LastHead)
	}
	// ...and nothing may have been created beside the caller.
	if _, err := os.Stat(filepath.Join(elsewhere, "space", ConfigFileName)); err == nil {
		t.Error("a stray config.yaml was written relative to the calling directory")
	}
}

func TestRecordAnchor(t *testing.T) {
	cfg := &Config{SpaceRoot: t.TempDir()}
	now := time.Now().UTC()

	if !cfg.RecordAnchor("api", "/home/me/projects/api", now) {
		t.Fatal("a first anchor is a change")
	}
	if len(cfg.Anchors) != 1 {
		t.Fatalf("got %d anchors, want 1", len(cfg.Anchors))
	}

	// Activating in the same place again must not rewrite the config every time.
	if cfg.RecordAnchor("api", "/home/me/projects/api", now.Add(time.Hour)) {
		t.Error("an unchanged path should report no change")
	}

	// A moved checkout replaces the path rather than adding a rival entry that
	// leaves the graph guessing which one is live.
	if !cfg.RecordAnchor("api", "/srv/work/api", now) {
		t.Error("a moved checkout is a change")
	}
	if len(cfg.Anchors) != 1 {
		t.Fatalf("got %d anchors after a move, want 1", len(cfg.Anchors))
	}
	if got, ok := cfg.AnchorFor("api"); !ok || got != "/srv/work/api" {
		t.Errorf("AnchorFor = %q %v, want the new path", got, ok)
	}

	if _, ok := cfg.AnchorFor("unknown"); ok {
		t.Error("AnchorFor invented a project that was never anchored")
	}
	if cfg.RecordAnchor("", "/x", now) || cfg.RecordAnchor("p", "", now) {
		t.Error("empty project or path must not be recorded")
	}
}
