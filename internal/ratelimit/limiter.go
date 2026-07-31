package ratelimit

import (
	"sync"
	"time"
)

// Config is server rate-limit knobs.
type Config struct {
	Enabled           bool `yaml:"enabled"`
	RequestsPerMinute int  `yaml:"requests_per_minute"` // per-user (or IP); default 120
	AuthPerMinute     int  `yaml:"auth_per_minute"`     // login endpoints; default 10
}

// Default returns enabled 120 rpm / 10 auth rpm.
func Default() Config {
	return Config{Enabled: true, RequestsPerMinute: 120, AuthPerMinute: 10}
}

type bucket struct {
	mu       sync.Mutex
	tokens   float64
	last     time.Time
	capacity float64
	rate     float64 // tokens per second
}

// Buckets are keyed by user or address, so the map grows with every distinct
// caller and would otherwise never shrink — a caller cycling addresses could
// spend our memory. Idle buckets are dropped: a bucket refills to full capacity
// long before the idle window elapses, so forgetting it changes no decision.
const (
	idleAfter  = 10 * time.Minute
	sweepEvery = 30 * time.Second
	sweepAbove = 1024
)

// Limiter is an in-process token bucket keyed by string.
type Limiter struct {
	cfg       Config
	mu        sync.Mutex
	m         map[string]*bucket
	lastSweep time.Time
}

// New builds a limiter (no-op if disabled).
func New(cfg Config) *Limiter {
	if cfg.RequestsPerMinute <= 0 {
		cfg.RequestsPerMinute = 120
	}
	if cfg.AuthPerMinute <= 0 {
		cfg.AuthPerMinute = 10
	}
	return &Limiter{cfg: cfg, m: map[string]*bucket{}}
}

// Allow consumes one token for key. ok=false → rate limited; retryAfter in seconds.
func (l *Limiter) Allow(key string, authEndpoint bool) (ok bool, limit, remaining int, resetUnix int64, retryAfter int) {
	if l == nil || !l.cfg.Enabled {
		return true, 0, 0, 0, 0
	}
	rpm := l.cfg.RequestsPerMinute
	if authEndpoint {
		rpm = l.cfg.AuthPerMinute
	}
	limit = rpm
	b := l.get(key, float64(rpm))
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	resetUnix = now.Add(time.Minute).Unix()
	if b.tokens < 1 {
		need := (1 - b.tokens) / b.rate
		if need < 1 {
			need = 1
		}
		return false, limit, 0, resetUnix, int(need + 0.999)
	}
	b.tokens--
	remaining = int(b.tokens)
	return true, limit, remaining, resetUnix, 0
}

func (l *Limiter) get(key string, rpm float64) *bucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.m[key]; ok {
		return b
	}
	now := time.Now()
	l.sweepLocked(now)
	b := &bucket{
		tokens:   rpm,
		last:     now,
		capacity: rpm,
		rate:     rpm / 60.0,
	}
	l.m[key] = b
	return b
}

func (l *Limiter) sweepLocked(now time.Time) {
	if len(l.m) < sweepAbove || now.Sub(l.lastSweep) < sweepEvery {
		return
	}
	l.lastSweep = now
	for k, b := range l.m {
		b.mu.Lock()
		idle := now.Sub(b.last) > idleAfter
		b.mu.Unlock()
		if idle {
			delete(l.m, k)
		}
	}
}

// Size reports how many buckets are tracked (for tests and diagnostics).
func (l *Limiter) Size() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}
