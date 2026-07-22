package space_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abyssmemes/contextverse/internal/space"
)

func TestCreateAndInspect(t *testing.T) {
	root := t.TempDir()
	err := space.Create(space.CreateOptions{
		SpaceRoot: root,
		Identity: space.IdentityFields{
			Name:     "Eduard",
			Role:     "DevOps",
			Language: "English",
			Tools:    "Go, Cursor",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	st, err := space.Inspect(root)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !st.Exists {
		t.Fatal("expected space to exist")
	}
	if len(st.Missing) != 0 {
		t.Fatalf("missing files: %v", st.Missing)
	}
	if st.IdentityName != "Eduard" {
		t.Fatalf("identity name = %q", st.IdentityName)
	}

	if _, err := os.Stat(filepath.Join(root, "template.yaml")); !os.IsNotExist(err) {
		t.Fatalf("template.yaml should be removed from live space, err=%v", err)
	}

	if err := space.UpdateIndex(root); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
}
