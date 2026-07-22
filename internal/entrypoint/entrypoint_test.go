package entrypoint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abyssmemes/contextverse/internal/entrypoint"
	"github.com/abyssmemes/contextverse/internal/space"
)

func TestGenerate(t *testing.T) {
	spaceRoot := t.TempDir()
	target := t.TempDir()

	if err := space.Create(space.CreateOptions{
		SpaceRoot: spaceRoot,
		Identity:  space.IdentityFields{Name: "Eduard", Role: "DevOps", Language: "English"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := entrypoint.Generate(entrypoint.Options{
		SpaceRoot: spaceRoot,
		TargetDir: target,
		Project:   "meow",
		Silent:    true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %v", res.Files)
	}

	claude, err := os.ReadFile(filepath.Join(target, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claude), spaceRoot) {
		t.Fatalf("CLAUDE.md missing space root")
	}
	if !strings.Contains(string(claude), "projects/meow/project.md") {
		t.Fatalf("CLAUDE.md missing project pointer")
	}

	rule, err := os.ReadFile(filepath.Join(target, ".cursor", "rules", "contextverse.mdc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rule), "alwaysApply: true") {
		t.Fatalf("cursor rule missing alwaysApply")
	}
}
