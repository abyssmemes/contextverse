package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Caching authentication is only safe if a withdrawn credential stops working
// at once. These tests are about that, not about speed: each one changes the
// files underneath a warm cache and expects the very next call to notice.
//
// The external-change cases matter most. Anything that went through the store
// would be caught by an in-process invalidation; a cache that only handles that
// is wrong for an operator editing users.yaml by hand, and wrong for a second
// contextd sharing the data directory.

func warmStore(t *testing.T) (*Store, string) {
	t.Helper()
	s := newStore(t)
	if err := s.AddUser("kim", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.CreateToken("kim", "api")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(tok); err != nil {
		t.Fatalf("warming the cache: %v", err)
	}
	return s, tok
}

func TestRevokedTokenStopsWorkingAtOnce(t *testing.T) {
	s, tok := warmStore(t)

	toks, err := s.ListTokens("kim")
	if err != nil || len(toks) != 1 {
		t.Fatalf("expected one token, got %d (%v)", len(toks), err)
	}
	if err := s.RevokeToken(toks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(tok); err == nil {
		t.Fatal("a revoked token still authenticates; the cache is serving a withdrawn credential")
	}
}

// The file removed by something other than this process — another contextd on
// the same data directory, or an operator with rm.
func TestTokenFileRemovedOutsideTheStoreStopsWorking(t *testing.T) {
	s, tok := warmStore(t)

	entries, err := os.ReadDir(s.tokensDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one token file, got %d (%v)", len(entries), err)
	}
	if err := os.Remove(filepath.Join(s.tokensDir(), entries[0].Name())); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(tok); err == nil {
		t.Fatal("a token whose file is gone still authenticates")
	}
}

// A token issued after the cache was warmed must work without waiting for
// anything to expire.
func TestNewTokenWorksAgainstAWarmCache(t *testing.T) {
	s, _ := warmStore(t)

	fresh, _, err := s.CreateToken("kim", "second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(fresh); err != nil {
		t.Fatalf("a newly issued token was refused: %v", err)
	}
}

// Grants edited on disk rather than through the store: the users cache is
// validated by stat, so this is the case that proves it re-reads.
func TestGrantsEditedOnDiskApplyToLiveTokens(t *testing.T) {
	s, tok := warmStore(t)

	raw, err := os.ReadFile(s.usersPath())
	if err != nil {
		t.Fatal(err)
	}
	var f usersFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f.Users[0].Policies = []string{"viewer"}
	f.Users[0].Role = RoleViewer
	out, err := yaml.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	// Written in place by a third party — no rename, no store call.
	if err := os.WriteFile(s.usersPath(), out, 0o600); err != nil {
		t.Fatal(err)
	}
	// Timestamps have finite resolution; make sure the change is visible as one.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(s.usersPath(), future, future); err != nil {
		t.Fatal(err)
	}

	p, err := s.Authenticate(tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Policies) != 1 || p.Policies[0] != "viewer" {
		t.Fatalf("a demotion written straight to users.yaml was not seen: %v", p.Policies)
	}
}

// Disabling goes through the store, and must take effect on the token already
// in the cache.
func TestDisablingAUserBeatsTheCache(t *testing.T) {
	s, tok := warmStore(t)

	if err := s.SetDisabled("kim", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(tok); err == nil {
		t.Fatal("a disabled user still authenticates from cache")
	}
}

// Authenticate is the one call every request makes, so it is also the one that
// will be made from every goroutine at once.
func TestAuthenticateIsSafeUnderConcurrency(t *testing.T) {
	s, tok := warmStore(t)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Authenticate(tok); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

// BenchmarkAuthenticate is why the caches exist: the cost used to scale with
// the number of credentials the server had ever issued, because answering one
// request meant opening every token file on disk.
//
// It settles the cache before timing. Tokens minted milliseconds earlier leave
// the directory's timestamp inside the window where a coarse filesystem could
// give a second change the same stamp, and the cache deliberately rescans while
// that is true — measuring there would measure the guard, not the steady state
// a server spends its life in. BenchmarkAuthenticateAfterAChange covers the
// other side.
func BenchmarkAuthenticate(b *testing.B) {
	for _, tokens := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("tokens=%d", tokens), func(b *testing.B) {
			s, tok := benchStore(b, tokens)
			settle(b, s, tok)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Authenticate(tok); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAuthenticateAfterAChange is the worst case: every call re-reads the
// directory because a token was just issued or revoked. This is what the old
// code did on every request, always.
func BenchmarkAuthenticateAfterAChange(b *testing.B) {
	for _, tokens := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("tokens=%d", tokens), func(b *testing.B) {
			s, tok := benchStore(b, tokens)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.dropTokens() // force the full reconcile
				if _, err := s.Authenticate(tok); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchStore(b *testing.B, tokens int) (*Store, string) {
	b.Helper()
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	if err := s.AddUser("kim", RoleAdmin); err != nil {
		b.Fatal(err)
	}
	var first string
	for i := 0; i < tokens; i++ {
		t, _, err := s.CreateToken("kim", "bench")
		if err != nil {
			b.Fatal(err)
		}
		if i == 0 {
			first = t // the oldest, so a scan pays the worst case
		}
	}
	return s, first
}

// settle waits out the window in which the token directory's timestamp is too
// fresh to be trusted, then warms the cache.
func settle(b *testing.B, s *Store, tok string) {
	b.Helper()
	time.Sleep(1100 * time.Millisecond)
	for i := 0; i < 2; i++ {
		if _, err := s.Authenticate(tok); err != nil {
			b.Fatal(err)
		}
	}
}
