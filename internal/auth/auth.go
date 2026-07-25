package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/abyssmemes/contextverse/internal/authz"
)

// Role is a coarse Phase-2a role.
type Role string

const (
	RoleAdmin       Role = "admin"
	RoleSpaceLead   Role = "space-lead"
	RoleContributor Role = "contributor"
	RoleViewer      Role = "viewer"
	RoleAuditor     Role = "auditor"
)

// User is a named account on the server.
type User struct {
	Name         string    `yaml:"name" json:"name"`
	Role         Role      `yaml:"role,omitempty" json:"role,omitempty"` // legacy alias → policies
	Policies     []string  `yaml:"policies,omitempty" json:"policies,omitempty"`
	PasswordHash string    `yaml:"password_hash,omitempty" json:"-"`
	CreatedAt    time.Time `yaml:"created_at" json:"created_at"`
	Disabled     bool      `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// EffectivePolicies returns attached policies (falls back to role name).
func (u User) EffectivePolicies() []string {
	if len(u.Policies) > 0 {
		return append([]string{}, u.Policies...)
	}
	if u.Role != "" {
		return []string{string(u.Role)}
	}
	return nil
}

type usersFile struct {
	Users []User `yaml:"users"`
}

// TokenRecord is stored hashed on disk.
type TokenRecord struct {
	ID        string    `json:"id"`
	User      string    `json:"user"`
	Role      Role      `json:"role,omitempty"`
	Policies  []string  `json:"policies,omitempty"`
	Label     string    `json:"label,omitempty"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"` // zero = never
}

// Expired reports whether the record is past its lifetime at t.
func (t TokenRecord) Expired(at time.Time) bool {
	return !t.ExpiresAt.IsZero() && at.After(t.ExpiresAt)
}

// EffectivePolicies returns token policies (falls back to role).
func (t TokenRecord) EffectivePolicies() []string {
	if len(t.Policies) > 0 {
		return append([]string{}, t.Policies...)
	}
	if t.Role != "" {
		return []string{string(t.Role)}
	}
	return nil
}

// Principal is the authenticated caller.
type Principal struct {
	User     string
	Role     Role // legacy primary role label
	Policies []string
	Token    string // plaintext presented (not stored)
	ID       string // token id
}

// Store manages users.yaml + token files under <dataDir>/auth.
type Store struct {
	mu       sync.RWMutex
	dataDir  string
	tokenTTL time.Duration
	failures loginFailures
}

// SetTokenTTL sets the lifetime stamped on newly issued tokens (0 = never expire).
func (s *Store) SetTokenTTL(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d < 0 {
		d = 0
	}
	s.tokenTTL = d
}

// TokenTTL returns the configured token lifetime.
func (s *Store) TokenTTL() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokenTTL
}

// OpenStore ensures auth directories exist and seeds builtin policies.
func OpenStore(dataDir string) (*Store, error) {
	s := &Store{dataDir: dataDir}
	if err := os.MkdirAll(filepath.Join(dataDir, "auth", "tokens"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "auth", "policies"), 0o755); err != nil {
		return nil, err
	}
	if err := authz.SeedBuiltins(filepath.Join(dataDir, "auth", "policies"), ""); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.usersPath()); os.IsNotExist(err) {
		if err := s.saveUsers(usersFile{}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) usersPath() string { return filepath.Join(s.dataDir, "auth", "users.yaml") }
func (s *Store) tokensDir() string { return filepath.Join(s.dataDir, "auth", "tokens") }

func (s *Store) loadUsers() (usersFile, error) {
	raw, err := os.ReadFile(s.usersPath())
	if err != nil {
		return usersFile{}, err
	}
	var f usersFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return usersFile{}, err
	}
	return f, nil
}

func (s *Store) saveUsers(f usersFile) error {
	raw, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	path := s.usersPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// AddUser creates a user. Fails if name exists.
func (s *Store) AddUser(name string, role Role) error {
	if name == "" {
		return fmt.Errorf("user name required")
	}
	if !ValidRole(role) {
		return fmt.Errorf("invalid role %q", role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadUsers()
	if err != nil {
		return err
	}
	for _, u := range f.Users {
		if u.Name == name {
			return fmt.Errorf("user %q already exists", name)
		}
	}
	f.Users = append(f.Users, User{
		Name:      name,
		Role:      role,
		Policies:  []string{string(role)},
		CreatedAt: time.Now().UTC(),
	})
	return s.saveUsers(f)
}

// ListUsers returns all users.
func (s *Store) ListUsers() ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, err := s.loadUsers()
	if err != nil {
		return nil, err
	}
	return f.Users, nil
}

// SetRole updates a user's role.
func (s *Store) SetRole(name string, role Role) error {
	if !ValidRole(role) {
		return fmt.Errorf("invalid role %q", role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadUsers()
	if err != nil {
		return err
	}
	for i := range f.Users {
		if f.Users[i].Name == name {
			f.Users[i].Role = role
			f.Users[i].Policies = []string{string(role)}
			return s.saveUsers(f)
		}
	}
	return fmt.Errorf("user %q not found", name)
}

// SetDisabled suspends or restores a user. Disabling also revokes their tokens;
// Authenticate refuses a disabled user regardless, so a leftover token file
// cannot outlive the suspension.
func (s *Store) SetDisabled(name string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadUsers()
	if err != nil {
		return err
	}
	for i := range f.Users {
		if f.Users[i].Name == name {
			f.Users[i].Disabled = disabled
			if err := s.saveUsers(f); err != nil {
				return err
			}
			if disabled {
				return s.revokeUserTokensLocked(name)
			}
			return nil
		}
	}
	return fmt.Errorf("user %q not found", name)
}

// RemoveUser disables the user and revokes tokens.
func (s *Store) RemoveUser(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadUsers()
	if err != nil {
		return err
	}
	found := false
	out := f.Users[:0]
	for _, u := range f.Users {
		if u.Name == name {
			found = true
			continue
		}
		out = append(out, u)
	}
	if !found {
		return fmt.Errorf("user %q not found", name)
	}
	f.Users = out
	if err := s.saveUsers(f); err != nil {
		return err
	}
	return s.revokeUserTokensLocked(name)
}

// CreateToken issues a new bearer token with the store's configured TTL.
// Returns plaintext once.
func (s *Store) CreateToken(user, label string) (plaintext string, rec TokenRecord, err error) {
	return s.CreateTokenTTL(user, label, s.TokenTTL())
}

// CreateTokenTTL issues a bearer token that stops working after ttl (0 = never).
func (s *Store) CreateTokenTTL(user, label string, ttl time.Duration) (plaintext string, rec TokenRecord, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadUsers()
	if err != nil {
		return "", TokenRecord{}, err
	}
	var role Role
	var policies []string
	found := false
	for _, u := range f.Users {
		if u.Name == user {
			if u.Disabled {
				return "", TokenRecord{}, fmt.Errorf("user %q disabled", user)
			}
			role = u.Role
			policies = u.EffectivePolicies()
			if role == "" && len(policies) > 0 {
				role = Role(policies[0])
			}
			found = true
			break
		}
	}
	if !found {
		return "", TokenRecord{}, fmt.Errorf("user %q not found", user)
	}
	id, err := randomHex(8)
	if err != nil {
		return "", TokenRecord{}, err
	}
	secret, err := randomHex(16)
	if err != nil {
		return "", TokenRecord{}, err
	}
	plaintext = fmt.Sprintf("cv-%s-%s", user, secret)
	now := time.Now().UTC()
	rec = TokenRecord{
		ID:        id,
		User:      user,
		Role:      role,
		Policies:  policies,
		Label:     label,
		Hash:      hashToken(plaintext),
		CreatedAt: now,
	}
	if ttl > 0 {
		rec.ExpiresAt = now.Add(ttl)
	}
	path := filepath.Join(s.tokensDir(), id+".json")
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", TokenRecord{}, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", TokenRecord{}, err
	}
	return plaintext, rec, nil
}

// ErrTokenExpired is returned when a presented token is past its lifetime.
var ErrTokenExpired = fmt.Errorf("token expired")

// Authenticate resolves a bearer token to a principal. Grants are re-read from
// users.yaml on every call, so demoting or disabling a user takes effect
// immediately instead of waiting for their tokens to be revoked.
func (s *Store) Authenticate(token string) (*Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}
	h := []byte(hashToken(token))
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.tokensDir())
	if err != nil {
		return nil, err
	}
	var match *TokenRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.tokensDir(), e.Name()))
		if err != nil {
			continue
		}
		var rec TokenRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(rec.Hash), h) == 1 {
			found := rec
			match = &found
			break
		}
	}
	if match == nil {
		return nil, fmt.Errorf("invalid token")
	}
	if match.Expired(time.Now().UTC()) {
		return nil, ErrTokenExpired
	}
	u, err := s.findUserLocked(match.User)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("invalid token")
	}
	if u.Disabled {
		return nil, fmt.Errorf("user %q disabled", u.Name)
	}
	pols := u.EffectivePolicies()
	if len(pols) == 0 {
		pols = match.EffectivePolicies()
	}
	role := u.Role
	if role == "" && len(pols) > 0 {
		role = Role(pols[0])
	}
	return &Principal{User: match.User, Role: role, Policies: pols, Token: token, ID: match.ID}, nil
}

func (s *Store) findUserLocked(name string) (*User, error) {
	f, err := s.loadUsers()
	if err != nil {
		return nil, err
	}
	for i := range f.Users {
		if f.Users[i].Name == name {
			u := f.Users[i]
			return &u, nil
		}
	}
	return nil, nil
}

// PruneExpiredTokens deletes token files whose lifetime has passed.
func (s *Store) PruneExpiredTokens() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.tokensDir())
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.tokensDir(), e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec TokenRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.Expired(now) {
			if os.Remove(path) == nil {
				n++
			}
		}
	}
	return n, nil
}

// ListTokens returns token metadata (no plaintext).
func (s *Store) ListTokens(userFilter string) ([]TokenRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.tokensDir())
	if err != nil {
		return nil, err
	}
	var out []TokenRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.tokensDir(), e.Name()))
		if err != nil {
			continue
		}
		var rec TokenRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if userFilter != "" && rec.User != userFilter {
			continue
		}
		rec.Hash = "" // never expose hash in list UX either — keep empty
		out = append(out, rec)
	}
	return out, nil
}

// RevokeToken deletes a token by id or plaintext prefix match on id.
func (s *Store) RevokeToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.tokensDir(), id+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("token %q not found", id)
		}
		return err
	}
	return nil
}

// RevokePrincipalToken revokes the token used by the principal.
func (s *Store) RevokePrincipalToken(p *Principal) error {
	if p == nil || p.ID == "" {
		return fmt.Errorf("no token id")
	}
	return s.RevokeToken(p.ID)
}

func (s *Store) revokeUserTokensLocked(user string) error {
	entries, err := os.ReadDir(s.tokensDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.tokensDir(), e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec TokenRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.User == user {
			_ = os.Remove(path)
		}
	}
	return nil
}

// CanWrite reports whether role may mutate space content (legacy helper).
func CanWrite(role Role) bool {
	return role == RoleAdmin || role == RoleSpaceLead || role == RoleContributor
}

// CanAdmin reports whether role may manage users/spaces (legacy helper).
func CanAdmin(role Role) bool {
	return role == RoleAdmin
}

// ValidRole checks role string.
func ValidRole(role Role) bool {
	switch role {
	case RoleAdmin, RoleSpaceLead, RoleContributor, RoleViewer, RoleAuditor:
		return true
	default:
		return false
	}
}

func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
