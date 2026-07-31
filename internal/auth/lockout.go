package auth

import (
	"fmt"
	"sync"
	"time"
)

// Lockout thresholds for password login. In-process: a restart clears the
// counters, which is acceptable because the window is short.
//
// # Who gets locked out
//
// Counting failures against the username alone means anyone who knows a name can
// lock its owner out by failing five times — a denial of service delivered by
// typing the wrong password, and one that a support desk cannot distinguish from
// a real attack. That is worse than the attack it prevents, because the attacker
// pays nothing and the victim loses access.
//
// So failures count twice: against the account, and against the address they
// came from. An account lock still exists, because an attacker spread across
// many addresses has to be stopped somewhere, but it is deliberately the looser
// of the two — a wider budget, so a single hostile client hits its own limit
// long before it can spend the account's.
//
// # What this does not solve
//
// The counters live in one process. A fleet behind a load balancer gives an
// attacker a fresh budget per replica, and a restart clears everything. Fixing
// that needs shared state, which the OSS server deliberately does not have —
// contextverse-server.md calls its HA "stateless, no clustering". An operator
// running more than one replica should rate-limit authentication at the router;
// the values here are a floor, not a fleet-wide guarantee.
const (
	// MaxLoginFailures is the per-address budget: the one an attacker spends.
	MaxLoginFailures = 5
	// MaxAccountFailures is the per-account budget. Larger on purpose: reaching
	// it means failures arrived from several addresses, which is the case the
	// account lock exists for.
	MaxAccountFailures = 25
	LockoutWindow      = 15 * time.Minute
	LockoutDuration    = 15 * time.Minute
)

// BootstrapTokenTTL bounds the first-run admin token, which is written to disk
// in plaintext so the operator can pick it up.
const BootstrapTokenTTL = 24 * time.Hour

// ErrLockedOut is returned while a subject is temporarily refused.
var ErrLockedOut = fmt.Errorf("too many failed logins, try again later")

type failState struct {
	count   int
	first   time.Time
	blocked time.Time
}

type loginFailures struct {
	mu sync.Mutex
	by map[string]*failState
}

func (l *loginFailures) locked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.by[key]
	if st == nil {
		return false
	}
	if !st.blocked.IsZero() && now.Before(st.blocked) {
		return true
	}
	if !st.blocked.IsZero() {
		delete(l.by, key)
	}
	return false
}

func (l *loginFailures) fail(key string, now time.Time, max int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.by == nil {
		l.by = map[string]*failState{}
	}
	st := l.by[key]
	if st == nil || now.Sub(st.first) > LockoutWindow {
		l.by[key] = &failState{count: 1, first: now}
		return
	}
	st.count++
	if st.count >= max {
		st.blocked = now.Add(LockoutDuration)
	}
	// Opportunistic eviction so a scripted attacker cannot grow the map without
	// bound by cycling usernames.
	if len(l.by) > 4096 {
		for k, v := range l.by {
			if now.Sub(v.first) > LockoutWindow && (v.blocked.IsZero() || now.After(v.blocked)) {
				delete(l.by, k)
			}
		}
	}
}

func (l *loginFailures) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.by, key)
}

// LoginLocked reports whether password login for username is currently refused.
func (s *Store) LoginLocked(username string) bool {
	return s.failures.locked(accountKey(username), time.Now())
}

// Namespaced so a username can never collide with an address — "10.0.0.1" is a
// legal username.
func accountKey(username string) string { return "user:" + username }
func addressKey(addr string) string     { return "addr:" + addr }
