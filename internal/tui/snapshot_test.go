package tui

import "testing"

func TestLoadSnapshotEmpty(t *testing.T) {
	s := LoadSnapshot(t.TempDir())
	if s.SpaceRoot == "" {
		t.Fatal("expected space root")
	}
	if len(s.Layers) != 3 {
		t.Fatalf("layers=%d", len(s.Layers))
	}
}
