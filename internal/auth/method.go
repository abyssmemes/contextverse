package auth

import "sync"

// Method is an OSS auth-method hook. Built-ins: token, userpass.
// OIDC/MFA are not registered here — cloud control plane only.
type Method interface {
	Name() string
	Enabled() bool
}

// Registry holds enabled auth methods for the data plane.
type Registry struct {
	mu      sync.RWMutex
	methods map[string]Method
}

// DefaultRegistry returns token + userpass.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(tokenMethod{})
	r.Register(userpassMethod{})
	return r
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{methods: make(map[string]Method)}
}

// Register adds or replaces a method by name.
func (r *Registry) Register(m Method) {
	if r == nil || m == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods[m.Name()] = m
}

// Get returns a method by name.
func (r *Registry) Get(name string) (Method, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.methods[name]
	return m, ok
}

// Names returns registered method names (unsorted).
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.methods))
	for n, m := range r.methods {
		if m.Enabled() {
			out = append(out, n)
		}
	}
	return out
}

type tokenMethod struct{}

func (tokenMethod) Name() string  { return "token" }
func (tokenMethod) Enabled() bool { return true }

type userpassMethod struct{}

func (userpassMethod) Name() string  { return "userpass" }
func (userpassMethod) Enabled() bool { return true }
