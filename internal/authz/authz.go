package authz

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Capability is an ACL verb on a path (create|read|update|delete|list|sudo|deny).
type Capability string

const (
	CapCreate Capability = "create"
	CapRead   Capability = "read"
	CapUpdate Capability = "update"
	CapDelete Capability = "delete"
	CapList   Capability = "list"
	CapSudo   Capability = "sudo"
	CapDeny   Capability = "deny"
)

// Rule is one path → capabilities entry.
type Rule struct {
	Path         string       `yaml:"path" json:"path"`
	Capabilities []Capability `yaml:"capabilities" json:"capabilities"`
}

// Policy is a named set of path → capability rules.
type Policy struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Builtin     bool   `yaml:"builtin,omitempty" json:"builtin,omitempty"`
	Rules       []Rule `yaml:"rules" json:"rules"`
}

// Engine loads policies from disk and evaluates Allow.
type Engine struct {
	mu       sync.RWMutex
	dir      string
	policies map[string]*Policy
}

// Open loads all *.yaml from dir (creates dir if missing).
func Open(dir string) (*Engine, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	e := &Engine{dir: dir, policies: map[string]*Policy{}}
	if err := e.Reload(); err != nil {
		return nil, err
	}
	return e, nil
}

// Dir returns the policies directory.
func (e *Engine) Dir() string { return e.dir }

// Reload re-reads policy files from disk.
func (e *Engine) Reload() error {
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		return err
	}
	next := map[string]*Policy{}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(e.dir, ent.Name()))
		if err != nil {
			return err
		}
		var p Policy
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("parse %s: %w", ent.Name(), err)
		}
		if p.Name == "" {
			p.Name = strings.TrimSuffix(ent.Name(), ".yaml")
		}
		cp := p
		next[p.Name] = &cp
	}
	e.mu.Lock()
	e.policies = next
	e.mu.Unlock()
	return nil
}

// Get returns a policy by name.
func (e *Engine) Get(name string) (*Policy, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.policies[name]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// List returns policy names sorted.
func (e *Engine) List() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.policies))
	for n := range e.policies {
		out = append(out, n)
	}
	// stable-ish: simple insertion sort for small N
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1] > out[j] {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}

// Write persists a policy and reloads.
func (e *Engine) Write(p Policy) error {
	if p.Name == "" {
		return fmt.Errorf("policy name required")
	}
	raw, err := yaml.Marshal(&p)
	if err != nil {
		return err
	}
	path := filepath.Join(e.dir, p.Name+".yaml")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return e.Reload()
}

// Delete removes a policy file (refuses builtin unless force).
func (e *Engine) Delete(name string, force bool) error {
	e.mu.RLock()
	p, ok := e.policies[name]
	e.mu.RUnlock()
	if ok && p.Builtin && !force {
		return fmt.Errorf("refusing to delete builtin policy %q (use force)", name)
	}
	path := filepath.Join(e.dir, name+".yaml")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("policy %q not found", name)
		}
		return err
	}
	return e.Reload()
}

// Vars used when expanding {{default}} etc. in rule paths.
type Vars map[string]string

// Allow reports whether the union of named policies grants cap on path.
// Deny-by-default; explicit deny wins; longest matching rule path wins per policy then union.
func (e *Engine) Allow(policyNames []string, path string, cap Capability, vars Vars) bool {
	if cap == CapDeny {
		return false
	}
	path = strings.TrimPrefix(path, "/")
	e.mu.RLock()
	defer e.mu.RUnlock()

	type hit struct {
		score int
		caps  []Capability
	}
	var hits []hit
	for _, name := range policyNames {
		p, ok := e.policies[name]
		if !ok {
			continue
		}
		bestScore := -1
		var bestCaps []Capability
		for _, rule := range p.Rules {
			pat := expand(rule.Path, vars)
			score, ok := matchPath(pat, path)
			if !ok {
				continue
			}
			if score > bestScore {
				bestScore = score
				bestCaps = rule.Capabilities
			}
		}
		if bestScore >= 0 {
			hits = append(hits, hit{score: bestScore, caps: bestCaps})
		}
	}
	if len(hits) == 0 {
		return false
	}
	// Most-specific matching rules win; deny in any matched rule denies.
	max := -1
	for _, h := range hits {
		if h.score > max {
			max = h.score
		}
	}
	var union []Capability
	for _, h := range hits {
		if h.score != max {
			continue
		}
		union = append(union, h.caps...)
	}
	// Deny from any matched rule.
	for _, h := range hits {
		for _, c := range h.caps {
			if c == CapDeny {
				return false
			}
		}
	}
	for _, c := range union {
		if c == cap {
			return true
		}
	}
	return false
}

// RequireSudo is true if any most-specific matching rule includes sudo (for ops that need it).
func (e *Engine) HasSudo(policyNames []string, path string, vars Vars) bool {
	return e.Allow(policyNames, path, CapSudo, vars) || e.hasCapExact(policyNames, path, CapSudo, vars)
}

func (e *Engine) hasCapExact(policyNames []string, path string, cap Capability, vars Vars) bool {
	path = strings.TrimPrefix(path, "/")
	e.mu.RLock()
	defer e.mu.RUnlock()
	max := -1
	var union []Capability
	for _, name := range policyNames {
		p, ok := e.policies[name]
		if !ok {
			continue
		}
		for _, rule := range p.Rules {
			pat := expand(rule.Path, vars)
			score, ok := matchPath(pat, path)
			if !ok {
				continue
			}
			if score > max {
				max = score
				union = append([]Capability{}, rule.Capabilities...)
			} else if score == max {
				union = append(union, rule.Capabilities...)
			}
		}
	}
	for _, c := range union {
		if c == CapDeny {
			return false
		}
	}
	for _, c := range union {
		if c == cap {
			return true
		}
	}
	return false
}

func expand(pat string, vars Vars) string {
	out := pat
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// matchPath returns specificity score if pattern matches path.
// Patterns: exact, prefix*, or * (all). Score = len(literal prefix).
func matchPath(pattern, path string) (int, bool) {
	pattern = strings.TrimPrefix(pattern, "/")
	path = strings.TrimPrefix(path, "/")
	if pattern == "*" || pattern == "" {
		return 0, true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/") {
			return len(prefix), true
		}
		return 0, false
	}
	if pattern == path {
		return len(pattern) + 1, true // exact beats same-length prefix
	}
	return 0, false
}

// SeedBuiltins writes default role presets if missing ({{default}} placeholders kept).
func SeedBuiltins(dir, defaultSpaceHint string) error {
	_ = defaultSpaceHint // presets keep {{default}}; expansion at eval time
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, p := range BuiltinPolicies() {
		path := filepath.Join(dir, p.Name+".yaml")
		if _, err := os.Stat(path); err == nil {
			continue
		}
		raw, err := yaml.Marshal(&p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// BuiltinPolicies returns the seeded preset policies.
func BuiltinPolicies() []Policy {
	return []Policy{
		{
			Name:        "admin",
			Description: "Full access including sys (sudo)",
			Builtin:     true,
			Rules: []Rule{
				{Path: "*", Capabilities: []Capability{CapCreate, CapRead, CapUpdate, CapDelete, CapList, CapSudo}},
			},
		},
		{
			Name:        "space-lead",
			Description: "Manage default space; no user/backend admin",
			Builtin:     true,
			Rules: []Rule{
				{Path: "spaces/", Capabilities: []Capability{CapList}},
				{Path: "spaces/{{default}}", Capabilities: []Capability{CapRead, CapUpdate, CapDelete}},
				{Path: "spaces/{{default}}/files", Capabilities: []Capability{CapList}},
				{Path: "spaces/{{default}}/files/*", Capabilities: []Capability{CapCreate, CapRead, CapUpdate, CapDelete, CapList}},
				{Path: "spaces/{{default}}/files/identity/*", Capabilities: []Capability{CapRead}},
				{Path: "spaces/{{default}}/head", Capabilities: []Capability{CapRead, CapUpdate}},
				{Path: "spaces/{{default}}/push", Capabilities: []Capability{CapUpdate}},
				{Path: "spaces/{{default}}/history/*", Capabilities: []Capability{CapRead, CapList, CapCreate}},
				{Path: "sys/health", Capabilities: []Capability{CapRead}},
			},
		},
		{
			Name:        "contributor",
			Description: "Write default space except identity/; no sys admin",
			Builtin:     true,
			Rules: []Rule{
				{Path: "spaces/", Capabilities: []Capability{CapList}},
				{Path: "spaces/{{default}}", Capabilities: []Capability{CapRead}},
				{Path: "spaces/{{default}}/files", Capabilities: []Capability{CapList}},
				{Path: "spaces/{{default}}/files/*", Capabilities: []Capability{CapCreate, CapRead, CapUpdate, CapDelete, CapList}},
				{Path: "spaces/{{default}}/files/identity/*", Capabilities: []Capability{CapRead}},
				{Path: "spaces/{{default}}/head", Capabilities: []Capability{CapRead, CapUpdate}},
				{Path: "spaces/{{default}}/push", Capabilities: []Capability{CapUpdate}},
				{Path: "sys/health", Capabilities: []Capability{CapRead}},
			},
		},
		{
			Name:        "viewer",
			Description: "Read-only on default space",
			Builtin:     true,
			Rules: []Rule{
				{Path: "spaces/", Capabilities: []Capability{CapList}},
				{Path: "spaces/{{default}}", Capabilities: []Capability{CapRead}},
				{Path: "spaces/{{default}}/files", Capabilities: []Capability{CapList}},
				{Path: "spaces/{{default}}/files/*", Capabilities: []Capability{CapRead, CapList}},
				{Path: "spaces/{{default}}/head", Capabilities: []Capability{CapRead}},
				{Path: "sys/health", Capabilities: []Capability{CapRead}},
			},
		},
	}
}
