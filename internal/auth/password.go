package auth

import (
	"fmt"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SetPassword stores a bcrypt hash for the user (userpass auth).
func (s *Store) SetPassword(name, password string) error {
	if name == "" {
		return fmt.Errorf("user name required")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadUsers()
	if err != nil {
		return err
	}
	for i := range f.Users {
		if f.Users[i].Name == name {
			f.Users[i].PasswordHash = string(hash)
			return s.saveUsers(f)
		}
	}
	return fmt.Errorf("user %q not found", name)
}

// SetPolicies replaces the user's policy list (and syncs Role to first policy).
func (s *Store) SetPolicies(name string, policies []string) error {
	if name == "" {
		return fmt.Errorf("user name required")
	}
	if len(policies) == 0 {
		return fmt.Errorf("at least one policy required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadUsers()
	if err != nil {
		return err
	}
	for i := range f.Users {
		if f.Users[i].Name == name {
			f.Users[i].Policies = append([]string{}, policies...)
			f.Users[i].Role = Role(policies[0])
			return s.saveUsers(f)
		}
	}
	return fmt.Errorf("user %q not found", name)
}

// errInvalidCredentials is the single answer for every failed login: a wrong
// password, an unknown user, a disabled user and a token-only user must be
// indistinguishable to the caller.
var errInvalidCredentials = fmt.Errorf("invalid credentials")

// dummyHash is a real bcrypt hash of an unguessable value. Comparing against it
// costs the same as comparing against a stored hash, so a missing user does not
// answer faster than a wrong password.
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// LoginUserpass verifies password and issues a new bearer token (shown once).
//
// Deprecated in favour of LoginUserpassFrom, which can also count failures
// against where they came from. Kept so callers that genuinely have no address —
// a local console, a test — do not have to invent one.
func (s *Store) LoginUserpass(username, password string) (plaintext string, rec TokenRecord, err error) {
	return s.LoginUserpassFrom(username, password, "")
}

// LoginUserpassFrom verifies a password and issues a bearer token, counting a
// failure against both the account and the address it came from.
//
// addr may be empty when the caller has none. Failures then count only against
// the account, which is the old behaviour and the weaker of the two.
func (s *Store) LoginUserpassFrom(username, password, addr string) (plaintext string, rec TokenRecord, err error) {
	if username == "" || password == "" {
		return "", TokenRecord{}, fmt.Errorf("username and password required")
	}
	now := time.Now()
	// The address is checked first and refused hardest: it is the budget an
	// attacker actually spends, and the one that cannot be used to lock out
	// somebody else.
	if addr != "" && s.failures.locked(addressKey(addr), now) {
		return "", TokenRecord{}, ErrLockedOut
	}
	if s.failures.locked(accountKey(username), now) {
		return "", TokenRecord{}, ErrLockedOut
	}
	s.mu.RLock()
	f, loadErr := s.loadUsers()
	s.mu.RUnlock()
	if loadErr != nil {
		return "", TokenRecord{}, loadErr
	}
	var u *User
	for i := range f.Users {
		if f.Users[i].Name == username {
			u = &f.Users[i]
			break
		}
	}
	hash := dummyHash
	usable := false
	if u != nil && !u.Disabled && u.PasswordHash != "" {
		hash = []byte(u.PasswordHash)
		usable = true
	}
	matched := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if !usable || !matched {
		s.failures.fail(accountKey(username), now, MaxAccountFailures)
		if addr != "" {
			s.failures.fail(addressKey(addr), now, MaxLoginFailures)
		}
		return "", TokenRecord{}, errInvalidCredentials
	}
	// A success clears the account, but not the address: one correct password
	// must not wipe the record of an attacker working through a list from the
	// same machine.
	s.failures.reset(accountKey(username))
	return s.CreateToken(username, "userpass")
}

// HasPassword reports whether the user has a password set.
func (s *Store) HasPassword(name string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, err := s.loadUsers()
	if err != nil {
		return false, err
	}
	for _, u := range f.Users {
		if u.Name == name {
			return u.PasswordHash != "", nil
		}
	}
	return false, fmt.Errorf("user %q not found", name)
}

// PoliciesDir returns <dataDir>/auth/policies.
func (s *Store) PoliciesDir() string {
	return filepath.Join(s.dataDir, "auth", "policies")
}
