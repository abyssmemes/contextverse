package storage

import (
	"io"
	"sync"
)

// Pool hands out Backends that outlive a single request.
//
// # Why this exists
//
// Open builds a connection every time it is called, and the server called it on
// every request that touched a space — often several times for one write (the
// put, the head bump, the quota walk). For the local and git drivers that was
// waste. For the shared drivers it was a fault:
//
//   - sql: each call ran Open + Ping + CREATE TABLE IF NOT EXISTS and produced a
//     *sql.DB that nothing ever closed. database/sql has no finaliser, so those
//     pools and their sockets lived until the process died. A server under
//     steady traffic walks into the database's connection limit within minutes.
//   - s3: each call built a client and issued HeadBucket, and CreateBucket when
//     that failed — a round trip, and a bill, per request.
//
// # What is shared and what is not
//
// Postgres is shared by DSN: one pool for the whole server, with each space
// getting a Prefixed view over it. That is the only driver where sharing the
// connection changes nothing about the key layout.
//
// s3, git and local are cached per space instead. Their space name is baked
// into the object prefix or the on-disk root, so hoisting it into a Prefixed
// wrapper would move every existing key — a migration, not a fix.
//
// The zero Pool is not usable; call NewPool.
type Pool struct {
	mu     sync.Mutex
	shared map[string]Backend // connection-level, keyed by DSN
	spaces map[string]Backend // per-space view, keyed by driver+root+name
	closed bool
}

// NewPool returns an empty pool.
func NewPool() *Pool {
	return &Pool{
		shared: map[string]Backend{},
		spaces: map[string]Backend{},
	}
}

// Open returns a Backend for opts, reusing a live one when it can.
//
// A failure is never cached: a database that was down when the first request
// arrived must not stay "down" for the life of the process.
func (p *Pool) Open(opts OpenOptions) (Backend, error) {
	if p == nil {
		return Open(opts)
	}
	driver := opts.Driver
	if driver == "" {
		driver = opts.Backend.Driver
	}
	if driver == "" {
		driver = DriverLocal
	}
	if driver == DriverSQL {
		return p.openSQL(opts)
	}
	return p.openPerSpace(driver, opts)
}

func (p *Pool) openPerSpace(driver string, opts OpenOptions) (Backend, error) {
	key := driver + "\x00" + opts.SpaceRoot + "\x00" + opts.SpaceName

	p.mu.Lock()
	if b, ok := p.spaces[key]; ok && !p.closed {
		p.mu.Unlock()
		return b, nil
	}
	p.mu.Unlock()

	// Opened outside the lock: s3 talks to the network here, and one slow space
	// must not stall every other space's requests.
	b, err := Open(opts)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		closeBackend(b)
		return b, nil
	}
	if existing, ok := p.spaces[key]; ok {
		closeBackend(b) // lost the race; keep the one already published
		return existing, nil
	}
	p.spaces[key] = b
	return b, nil
}

func (p *Pool) openSQL(opts OpenOptions) (Backend, error) {
	dsn := resolveSQLDSN(opts)

	p.mu.Lock()
	shared, ok := p.shared[dsn]
	closed := p.closed
	p.mu.Unlock()

	if !ok || closed {
		fresh, err := Open(OpenOptions{Driver: DriverSQL, Backend: opts.Backend})
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		if existing, dup := p.shared[dsn]; dup && !p.closed {
			p.mu.Unlock()
			closeBackend(fresh)
			shared = existing
		} else {
			if !p.closed {
				p.shared[dsn] = fresh
			}
			p.mu.Unlock()
			shared = fresh
		}
	}

	if opts.SpaceName == "" {
		return shared, nil
	}
	// Cheap value wrapper, not a connection: no reason to cache it.
	return &Prefixed{Inner: shared, Prefix: "spaces/" + opts.SpaceName}, nil
}

// Evict drops any cached per-space Backend for name and closes it. Called when
// a space is deleted, so a later space of the same name cannot inherit a handle
// to the old one.
func (p *Pool) Evict(name string) {
	if p == nil || name == "" {
		return
	}
	p.mu.Lock()
	var dropped []Backend
	for key, b := range p.spaces {
		// Keys are driver\x00root\x00name; match the name field exactly.
		if i := lastNul(key); i >= 0 && key[i+1:] == name {
			dropped = append(dropped, b)
			delete(p.spaces, key)
		}
	}
	p.mu.Unlock()
	for _, b := range dropped {
		closeBackend(b)
	}
}

// Close releases every backend the pool holds. Safe to call twice.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	all := make([]Backend, 0, len(p.shared)+len(p.spaces))
	for k, b := range p.shared {
		all = append(all, b)
		delete(p.shared, k)
	}
	for k, b := range p.spaces {
		all = append(all, b)
		delete(p.spaces, k)
	}
	p.mu.Unlock()

	var firstErr error
	for _, b := range all {
		if err := closeBackend(b); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func lastNul(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == 0 {
			return i
		}
	}
	return -1
}

// closeBackend closes a Backend that owns a connection.
//
// Deliberately does not unwrap Prefixed: a namespaced view is one of many over
// a shared pool, and closing through it would take the pool out from under
// every other space. The pool closes what it owns, which is always the
// connection-level backend.
func closeBackend(b Backend) error {
	if c, ok := b.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
