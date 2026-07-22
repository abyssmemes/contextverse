package authz

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// UserACLFile is <dataDir>/auth/acl.yaml — per-user path rules.
type UserACLFile struct {
	Users map[string][]Rule `yaml:"users"`
}

func (e *Engine) aclPath() string {
	// policies live in …/auth/policies → sibling acl.yaml
	return filepath.Join(filepath.Dir(e.dir), "acl.yaml")
}

// ReloadUserACL loads per-user rules from auth/acl.yaml.
func (e *Engine) ReloadUserACL() error {
	path := e.aclPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			e.mu.Lock()
			e.userACL = map[string][]Rule{}
			e.mu.Unlock()
			return nil
		}
		return err
	}
	var doc UserACLFile
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Users == nil {
		doc.Users = map[string][]Rule{}
	}
	e.mu.Lock()
	e.userACL = doc.Users
	e.mu.Unlock()
	return nil
}

func (e *Engine) saveUserACLLocked() error {
	doc := UserACLFile{Users: e.userACL}
	if doc.Users == nil {
		doc.Users = map[string][]Rule{}
	}
	raw, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	path := e.aclPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// UserRules returns a copy of per-user ACL rules.
func (e *Engine) UserRules(user string) []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rules := e.userACL[user]
	out := make([]Rule, len(rules))
	copy(out, rules)
	return out
}

// ListACLUsers returns usernames that have per-user rules.
func (e *Engine) ListACLUsers() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.userACL))
	for u := range e.userACL {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// SetUserRules replaces all per-user rules for user (empty clears).
func (e *Engine) SetUserRules(user string, rules []Rule) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return fmt.Errorf("user required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.userACL == nil {
		e.userACL = map[string][]Rule{}
	}
	if len(rules) == 0 {
		delete(e.userACL, user)
	} else {
		cp := make([]Rule, len(rules))
		copy(cp, rules)
		e.userACL[user] = cp
	}
	return e.saveUserACLLocked()
}

// AddUserRule appends one rule for user.
func (e *Engine) AddUserRule(user string, rule Rule) error {
	user = strings.TrimSpace(user)
	if user == "" || rule.Path == "" {
		return fmt.Errorf("user and path required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.userACL == nil {
		e.userACL = map[string][]Rule{}
	}
	e.userACL[user] = append(e.userACL[user], rule)
	return e.saveUserACLLocked()
}

// RemoveUserRulePath removes rules whose path matches (exact).
func (e *Engine) RemoveUserRulePath(user, path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	rules := e.userACL[user]
	out := rules[:0]
	for _, r := range rules {
		if r.Path != path {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		delete(e.userACL, user)
	} else {
		e.userACL[user] = out
	}
	return e.saveUserACLLocked()
}
