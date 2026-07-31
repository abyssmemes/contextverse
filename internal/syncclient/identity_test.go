package syncclient

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/spacesvc"
)

// The default policy: identity/ is seeded once and then left alone.
func defaultSync() spacesvc.SyncConfig {
	return spacesvc.SyncConfig{
		Default: "always",
		Rules: []spacesvc.SyncRule{
			{Path: "identity/", Mode: "init-only"},
		},
	}
}

// init-only means "seed this once, then it is yours". Pull honours that. Push
// does not: it skips only `never`, so a client sends identity/me.md — a real
// person's name, role, tools and preferences — up into the space everyone on
// the team pulls from.
//
// The asymmetry is what hides it. Your own machine never sees a change, because
// pull refuses to overwrite what it already seeded. The file still leaves.
func TestInitOnlyPathsAreNotPushedToTheSharedSpace(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("team/principles.md", "shared, and meant to be")
	write("identity/me.md", "Eduard, DevOps engineer, personal preferences")

	// nil state: nothing has been sent from this machine yet, which is the
	// first-push case and the one where everything eligible travels.
	ops, _, err := collectPushOps(root, defaultSync(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var pushed []string
	for _, o := range ops {
		pushed = append(pushed, o.Path)
	}
	for _, p := range pushed {
		if p == "identity/me.md" {
			t.Errorf("identity/me.md was pushed to the shared space; init-only means "+
				"seeded once and then private, and pull already treats it that way. "+
				"Pushed: %v", pushed)
		}
	}
	// The shared document still has to travel, or the fix has broken syncing.
	found := false
	for _, p := range pushed {
		if p == "team/principles.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("team/principles.md was not pushed; pushed: %v", pushed)
	}
}
