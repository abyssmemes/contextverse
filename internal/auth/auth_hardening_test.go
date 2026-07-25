package auth

import (
	"errors"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExpiredTokenIsRefused(t *testing.T) {
	s := newStore(t)
	if err := s.AddUser("kim", RoleContributor); err != nil {
		t.Fatal(err)
	}
	tok, rec, err := s.CreateTokenTTL("kim", "short", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ExpiresAt.IsZero() {
		t.Fatal("expected an expiry stamp")
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := s.Authenticate(tok); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token must be refused, got %v", err)
	}
	n, err := s.PruneExpiredTokens()
	if err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
}

func TestTokenTTLAppliesToNewTokens(t *testing.T) {
	s := newStore(t)
	s.SetTokenTTL(48 * time.Hour)
	if err := s.AddUser("kim", RoleContributor); err != nil {
		t.Fatal(err)
	}
	_, rec, err := s.CreateToken("kim", "api")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ExpiresAt.IsZero() {
		t.Fatal("store TTL must be stamped on new tokens")
	}
}

func TestAuthenticateFollowsCurrentGrants(t *testing.T) {
	s := newStore(t)
	if err := s.AddUser("kim", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.CreateToken("kim", "api")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPolicies("kim", []string{"viewer"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Policies) != 1 || p.Policies[0] != "viewer" {
		t.Fatalf("demotion must apply to live tokens, got %v", p.Policies)
	}
}

func TestAuthenticateRefusesDisabledUser(t *testing.T) {
	s := newStore(t)
	if err := s.AddUser("kim", RoleContributor); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.CreateToken("kim", "api")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled("kim", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(tok); err == nil {
		t.Fatal("a disabled user must not authenticate")
	}
}

func TestLoginFailuresAreIndistinguishableAndLockOut(t *testing.T) {
	s := newStore(t)
	if err := s.AddUser("kim", RoleContributor); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword("kim", "correct-horse"); err != nil {
		t.Fatal(err)
	}

	_, _, missing := s.LoginUserpass("nobody", "correct-horse")
	_, _, wrong := s.LoginUserpass("kim", "wrong-password")
	if missing == nil || wrong == nil {
		t.Fatal("both attempts must fail")
	}
	if missing.Error() != wrong.Error() {
		t.Fatalf("failure reasons must not differ: %q vs %q", missing, wrong)
	}

	for i := 0; i < MaxLoginFailures; i++ {
		_, _, _ = s.LoginUserpass("kim", "wrong-password")
	}
	if !s.LoginLocked("kim") {
		t.Fatal("expected lockout after repeated failures")
	}
	if _, _, err := s.LoginUserpass("kim", "correct-horse"); !errors.Is(err, ErrLockedOut) {
		t.Fatalf("locked account must refuse even the right password, got %v", err)
	}
}

func TestSuccessfulLoginClearsFailures(t *testing.T) {
	s := newStore(t)
	if err := s.AddUser("kim", RoleContributor); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword("kim", "correct-horse"); err != nil {
		t.Fatal(err)
	}
	_, _, _ = s.LoginUserpass("kim", "nope")
	if _, _, err := s.LoginUserpass("kim", "correct-horse"); err != nil {
		t.Fatal(err)
	}
	if s.LoginLocked("kim") {
		t.Fatal("a success must reset the counter")
	}
}
