package spacesvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/storage"
)

func newService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	svc := &Service{DataDir: dir, Backend: config.Backend{Driver: "local"}}
	if err := os.MkdirAll(svc.SpaceRoot("team"), 0o755); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestPutFileRefusesTraversal(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	if _, err := svc.PutFile(ctx, "team", "../../loot.md", []byte("x"), ""); err == nil {
		t.Fatal("write outside the space must be refused")
	}
	if _, err := os.Stat(filepath.Join(svc.DataDir, "loot.md")); !os.IsNotExist(err) {
		t.Fatal("a file was written outside the space tree")
	}
}

func TestSpaceNameIsValidated(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	if _, err := svc.SpaceRootFor("../etc"); !errors.Is(err, storage.ErrPathEscape) {
		t.Fatalf("space name traversal must be refused, got %v", err)
	}
	if _, err := svc.Create(ctx, "../evil", "", false); err == nil {
		t.Fatal("creating a space outside the spaces root must be refused")
	}
	if _, err := svc.LoadMeta("../../etc"); err == nil {
		t.Fatal("reading meta outside the spaces root must be refused")
	}
}

func TestPushRefusesTraversal(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	head, err := svc.Head(ctx, "team")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	_, err = svc.Push(ctx, "team", PushRequest{
		ExpectedHead: string(head),
		Ops:          []PushOp{{Op: "put", Path: "../../loot.md", ContentB64: "eA=="}},
	})
	if err == nil {
		t.Fatal("push outside the space must be refused")
	}
	if _, err := os.Stat(filepath.Join(svc.DataDir, "loot.md")); !os.IsNotExist(err) {
		t.Fatal("a file was written outside the space tree")
	}
}
