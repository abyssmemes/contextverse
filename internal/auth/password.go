package auth

import (
	"fmt"
	"path/filepath"

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

// LoginUserpass verifies password and issues a new bearer token (shown once).
func (s *Store) LoginUserpass(username, password string) (plaintext string, rec TokenRecord, err error) {
	if username == "" || password == "" {
		return "", TokenRecord{}, fmt.Errorf("username and password required")
	}
	s.mu.RLock()
	f, err := s.loadUsers()
	s.mu.RUnlock()
	if err != nil {
		return "", TokenRecord{}, err
	}
	var u *User
	for i := range f.Users {
		if f.Users[i].Name == username {
			u = &f.Users[i]
			break
		}
	}
	if u == nil {
		return "", TokenRecord{}, fmt.Errorf("invalid credentials")
	}
	if u.Disabled {
		return "", TokenRecord{}, fmt.Errorf("user disabled")
	}
	if u.PasswordHash == "" {
		return "", TokenRecord{}, fmt.Errorf("password login not configured for this user (set password or use a token)")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return "", TokenRecord{}, fmt.Errorf("invalid credentials")
	}
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
