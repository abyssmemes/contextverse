package auth

import (
	"fmt"
	"sync"
	"time"
)

// Lockout thresholds for password login. Single-node, in-process: a restart
// clears the counters, which is acceptable because the window is short.
const (
	MaxLoginFailures = 5
	LockoutWindow    = 15 * time.Minute
	LockoutDuration  = 15 * time.Minute
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

func (l *loginFailures) fail(key string, now time.Time) {
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
	if st.count >= MaxLoginFailures {
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
	return s.failures.locked(username, time.Now())
}
