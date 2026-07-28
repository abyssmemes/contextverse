package spacesvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/orkcom-tech/contextverse/internal/quotas"
)

// Per-space limits exist because server-wide ones are the right default and the
// wrong only option. One server holds a canonical space that should stay small
// and a scratch space that should not; a hosted fleet cannot give two customers
// on one instance different plans without this.

func serviceWithSpace(t *testing.T, server quotas.Config) (*Service, string) {
	t.Helper()
	svc := &Service{DataDir: t.TempDir(), Quotas: server}
	if _, err := svc.Create(context.Background(), "acme", "solo-default", true); err != nil {
		t.Fatal(err)
	}
	return svc, "acme"
}

func setSpaceQuotas(t *testing.T, svc *Service, name string, q quotas.Config) {
	t.Helper()
	meta, err := svc.LoadMeta(name)
	if err != nil {
		t.Fatal(err)
	}
	meta.Quotas = q
	if err := svc.SaveMeta(meta); err != nil {
		t.Fatal(err)
	}
}

// A space with no override behaves exactly as before.
func TestSpaceWithoutOverrideUsesTheServerLimits(t *testing.T) {
	server := quotas.Config{MaxFileSize: 1024, MaxSpaceSize: 1 << 20, MaxFiles: 10}
	svc, name := serviceWithSpace(t, server)

	got := svc.QuotasFor(name)
	if got != server {
		t.Errorf("got %+v, want the server limits %+v", got, server)
	}
}

// Overrides are per field, so a space can raise one limit without restating the
// others — otherwise every override has to duplicate the server config and
// silently freezes at whatever it was on the day it was written.
func TestOverridesApplyFieldByField(t *testing.T) {
	server := quotas.Config{MaxFileSize: 1024, MaxSpaceSize: 1 << 20, MaxFiles: 10}
	svc, name := serviceWithSpace(t, server)

	setSpaceQuotas(t, svc, name, quotas.Config{MaxFiles: 500})

	got := svc.QuotasFor(name)
	if got.MaxFiles != 500 {
		t.Errorf("MaxFiles = %d, want the override 500", got.MaxFiles)
	}
	if got.MaxFileSize != server.MaxFileSize {
		t.Errorf("MaxFileSize = %d, want the inherited %d", got.MaxFileSize, server.MaxFileSize)
	}
	if got.MaxSpaceSize != server.MaxSpaceSize {
		t.Errorf("MaxSpaceSize = %d, want the inherited %d", got.MaxSpaceSize, server.MaxSpaceSize)
	}
}

// The point of the whole change: two spaces on one server, two limits.
func TestTwoSpacesOnOneServerCanHaveDifferentLimits(t *testing.T) {
	svc := &Service{DataDir: t.TempDir(), Quotas: quotas.Config{MaxFileSize: 64, MaxSpaceSize: 1 << 20, MaxFiles: 100}}
	ctx := context.Background()
	for _, n := range []string{"tight", "roomy"} {
		if _, err := svc.Create(ctx, n, "solo-default", true); err != nil {
			t.Fatal(err)
		}
	}
	setSpaceQuotas(t, svc, "roomy", quotas.Config{MaxFileSize: 4096})

	big := []byte(strings.Repeat("x", 1000))

	if _, err := svc.PutFile(ctx, "tight", "notes.md", big, ""); err == nil {
		t.Error("the tight space accepted a file over its limit")
	} else {
		var qe *quotas.Exceeded
		if !errors.As(err, &qe) {
			t.Errorf("expected a quota error, got %v", err)
		} else if qe.Limit != 64 {
			t.Errorf("refused against limit %d, want the server's 64", qe.Limit)
		}
	}

	if _, err := svc.PutFile(ctx, "roomy", "notes.md", big, ""); err != nil {
		t.Errorf("the roomy space refused a file its own limit allows: %v", err)
	}
}

// A space whose metadata cannot be read must fall back to the server limits,
// not to none. Failing open on a quota check is how a misconfigured space
// becomes an unbounded one.
func TestUnreadableMetaFallsBackToTheServerLimits(t *testing.T) {
	server := quotas.Config{MaxFileSize: 1024, MaxSpaceSize: 1 << 20, MaxFiles: 10}
	svc := &Service{DataDir: t.TempDir(), Quotas: server}

	got := svc.QuotasFor("no-such-space")
	if got != server {
		t.Errorf("got %+v, want the server limits %+v — an unknown space must not be unlimited", got, server)
	}
}
