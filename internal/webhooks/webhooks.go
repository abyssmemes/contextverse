package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/abyssmemes/contextverse/internal/logx"
)

// Event is the shared outbound envelope (webhooks + future SSE).
type Event struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Created time.Time      `json:"created"`
	Space   string         `json:"space,omitempty"`
	Scope   string         `json:"scope,omitempty"`
	Actor   string         `json:"actor,omitempty"`
	Version string         `json:"version,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// Hook is one configured destination.
type Hook struct {
	ID      string   `yaml:"id" json:"id"`
	URL     string   `yaml:"url" json:"url"`
	Events  []string `yaml:"events" json:"events"` // empty = all; supports "*"
	Secret  string   `yaml:"secret" json:"-"`
	Space   string   `yaml:"space,omitempty" json:"space,omitempty"` // filter
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Format  string   `yaml:"format,omitempty" json:"format,omitempty"` // generic (only)
}

// Public returns a copy safe for API responses (secret redacted).
func (h Hook) Public() Hook {
	out := h
	if out.Secret != "" {
		out.Secret = ""
	}
	return out
}

type fileDoc struct {
	Webhooks []Hook `yaml:"webhooks"`
}

// Store persists hooks under <dataDir>/webhooks/webhooks.yaml.
type Store struct {
	mu  sync.Mutex
	dir string
	// Policy rejects unsafe destinations at write time so a bad hook is refused
	// with a clear message instead of failing on every delivery.
	Policy TargetPolicy
}

// Open creates the store directory.
func Open(dataDir string) (*Store, error) {
	dir := filepath.Join(dataDir, "webhooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path() string { return filepath.Join(s.dir, "webhooks.yaml") }

func (s *Store) deadLetterPath() string {
	return filepath.Join(s.dir, "dead-letter.jsonl")
}

// List returns configured hooks.
func (s *Store) List() ([]Hook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	return append([]Hook{}, doc.Webhooks...), nil
}

// Get returns a hook by id.
func (s *Store) Get(id string) (Hook, bool, error) {
	list, err := s.List()
	if err != nil {
		return Hook{}, false, err
	}
	for _, h := range list {
		if h.ID == id {
			return h, true, nil
		}
	}
	return Hook{}, false, nil
}

// Upsert creates or replaces a hook. Pass Enabled=false explicitly to disable.
func (s *Store) Upsert(h Hook) (Hook, error) {
	if h.URL == "" {
		return Hook{}, fmt.Errorf("url required")
	}
	if err := s.Policy.ValidateURL(h.URL); err != nil {
		return Hook{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return Hook{}, err
	}
	found := false
	for i := range doc.Webhooks {
		if doc.Webhooks[i].ID == h.ID && h.ID != "" {
			prev := doc.Webhooks[i]
			if h.Secret == "" {
				h.Secret = prev.Secret
			}
			doc.Webhooks[i] = h
			found = true
			break
		}
	}
	if !found {
		if h.ID == "" {
			h.ID = newID("wh")
		}
		if h.Secret == "" {
			h.Secret = newSecret()
		}
		// default enabled on create when zero-value bool is false and not in YAML —
		// callers should set Enabled=true for new hooks.
		doc.Webhooks = append(doc.Webhooks, h)
	}
	if err := s.saveLocked(doc); err != nil {
		return Hook{}, err
	}
	return h, nil
}

// Delete removes a hook.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	out := doc.Webhooks[:0]
	for _, h := range doc.Webhooks {
		if h.ID != id {
			out = append(out, h)
		}
	}
	doc.Webhooks = out
	return s.saveLocked(doc)
}

func (s *Store) loadLocked() (fileDoc, error) {
	raw, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return fileDoc{}, nil
		}
		return fileDoc{}, err
	}
	var doc fileDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fileDoc{}, err
	}
	return doc, nil
}

func (s *Store) saveLocked(doc fileDoc) error {
	raw, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

// Delivery worker pool sizing. Each attempt sequence sleeps through its
// backoffs, so one goroutine per event would let a burst of writes park
// thousands of sleeping goroutines and sockets. A small pool with a bounded
// queue puts a ceiling on both, and a full queue drops the delivery loudly
// rather than growing memory.
const (
	deliveryWorkers   = 4
	deliveryQueueSize = 256
)

// Dispatcher delivers events asynchronously.
type Dispatcher struct {
	Store  *Store
	Client *http.Client
	now    func() time.Time
	// OnEmit is called synchronously after the event is stamped (SSE hub, metrics).
	OnEmit func(Event)
	// OnDelivered is called after a webhook attempt settles (success or dead-letter).
	OnDelivered func(ok bool)
	// OnDropped is called when the delivery queue is full.
	OnDropped func(Event)
	// Policy decides which destinations may be dialled.
	Policy TargetPolicy

	queue     chan Event
	startPool sync.Once
}

// NewDispatcher builds a dispatcher with sensible defaults.
func NewDispatcher(store *Store) *Dispatcher {
	d := &Dispatcher{
		Store: store,
		now:   time.Now,
	}
	d.Client = d.Policy.Client(8 * time.Second)
	return d
}

// SetPolicy applies a destination policy and rebuilds the client so the socket
// check matches it.
func (d *Dispatcher) SetPolicy(p TargetPolicy) {
	d.Policy = p
	timeout := 8 * time.Second
	if d.Client != nil && d.Client.Timeout > 0 {
		timeout = d.Client.Timeout
	}
	d.Client = p.Client(timeout)
}

// Emit fans out to matching hooks (non-blocking).
func (d *Dispatcher) Emit(evt Event) {
	if d == nil {
		return
	}
	if evt.ID == "" {
		evt.ID = newID("evt")
	}
	if evt.Created.IsZero() {
		evt.Created = d.now().UTC()
	}
	if d.OnEmit != nil {
		d.OnEmit(evt)
	}
	if d.Store == nil {
		return
	}
	d.startPool.Do(d.spawnWorkers)
	select {
	case d.queue <- evt:
	default:
		logx.L().Warn("webhook delivery queue full, dropping event", "event", evt.ID, "type", evt.Type)
		if d.OnDropped != nil {
			d.OnDropped(evt)
		}
	}
}

// EmitSync delivers before returning. One-shot commands need it: a process that
// exits right after Emit would take the queue with it.
func (d *Dispatcher) EmitSync(evt Event) {
	if d == nil {
		return
	}
	if evt.ID == "" {
		evt.ID = newID("evt")
	}
	if evt.Created.IsZero() {
		evt.Created = d.now().UTC()
	}
	if d.OnEmit != nil {
		d.OnEmit(evt)
	}
	if d.Store == nil {
		return
	}
	d.deliverAll(evt)
}

func (d *Dispatcher) spawnWorkers() {
	d.queue = make(chan Event, deliveryQueueSize)
	for i := 0; i < deliveryWorkers; i++ {
		go func() {
			for evt := range d.queue {
				d.deliverAll(evt)
			}
		}()
	}
}

func (d *Dispatcher) deliverAll(evt Event) {
	hooks, err := d.Store.List()
	if err != nil {
		logx.L().Warn("webhooks list", "err", err)
		return
	}
	for _, h := range hooks {
		if !h.Enabled {
			continue
		}
		if h.Space != "" && h.Space != evt.Space {
			continue
		}
		if !matchEvent(h.Events, evt.Type) {
			continue
		}
		d.deliverWithRetry(h, evt)
	}
}

func matchEvent(want []string, typ string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if w == "*" || w == typ {
			return true
		}
		if strings.HasSuffix(w, ".*") && strings.HasPrefix(typ, strings.TrimSuffix(w, "*")) {
			return true
		}
	}
	return false
}

func (d *Dispatcher) deliverWithRetry(h Hook, evt Event) {
	backoffs := []time.Duration{time.Second, 5 * time.Second, 25 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		if attempt > 0 {
			time.Sleep(backoffs[attempt-1])
		}
		if err := d.postOnce(h, evt); err != nil {
			lastErr = err
			logx.L().Warn("webhook delivery", "hook", h.ID, "attempt", attempt+1, "err", err)
			continue
		}
		if d.OnDelivered != nil {
			d.OnDelivered(true)
		}
		return
	}
	if d.OnDelivered != nil {
		d.OnDelivered(false)
	}
	_ = d.Store.appendDeadLetter(DeadLetter{
		HookID:   h.ID,
		URL:      h.URL,
		Event:    evt,
		Error:    fmt.Sprint(lastErr),
		FailedAt: d.now().UTC(),
		Attempts: len(backoffs) + 1,
	})
}

// Test delivers a synthetic ping to one hook synchronously.
func (d *Dispatcher) Test(ctx context.Context, id string) error {
	h, ok, err := d.Store.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("webhook %q not found", id)
	}
	evt := Event{
		ID:      newID("evt"),
		Type:    "webhook.test",
		Created: d.now().UTC(),
		Actor:   "system",
		Data:    map[string]any{"hook_id": id},
	}
	return d.postOnce(h, evt)
}

func (d *Dispatcher) postOnce(h Hook, evt Event) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	ts := fmt.Sprintf("%d", d.now().UTC().Unix())
	sig := Sign(h.Secret, body)
	req, err := http.NewRequest(http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ContextVerse-Event", evt.Type)
	req.Header.Set("X-ContextVerse-Delivery", evt.ID)
	req.Header.Set("X-ContextVerse-Signature", sig)
	req.Header.Set("X-ContextVerse-Timestamp", ts)
	res, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}

// Sign returns sha256=<hex> HMAC of body.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks signature (constant-time).
func Verify(secret, header string, body []byte) bool {
	want := Sign(secret, body)
	return hmac.Equal([]byte(want), []byte(header))
}

// DeadLetter is a failed delivery record.
type DeadLetter struct {
	HookID   string    `json:"hook_id"`
	URL      string    `json:"url"`
	Event    Event     `json:"event"`
	Error    string    `json:"error"`
	FailedAt time.Time `json:"failed_at"`
	Attempts int       `json:"attempts"`
}

// A hook that is down during a busy hour writes one line per failed attempt, so
// the file is capped and rotated once. Two generations is enough to investigate
// an incident without letting a broken endpoint fill the disk.
const (
	maxDeadLetterBytes = 8 << 20
	deadLetterTailScan = 1 << 20
)

func (s *Store) rotatedDeadLetterPath() string {
	return filepath.Join(s.dir, "dead-letter.1.jsonl")
}

func (s *Store) appendDeadLetter(dl DeadLetter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(dl)
	if err != nil {
		return err
	}
	path := s.deadLetterPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return err
	}
	fi, statErr := f.Stat()
	f.Close()
	if statErr == nil && fi.Size() > maxDeadLetterBytes {
		// Rename rather than truncate so a reader mid-file keeps its handle.
		if err := os.Rename(path, s.rotatedDeadLetterPath()); err != nil {
			return err
		}
	}
	return nil
}

// ListDeadLetter returns recent dead-letter rows (newest last). Only the tail of
// each file is read, so a large backlog cannot turn a UI page load into a
// multi-megabyte allocation.
func (s *Store) ListDeadLetter(limit int) ([]DeadLetter, error) {
	out, err := readDeadLetterTail(s.deadLetterPath())
	if err != nil {
		return nil, err
	}
	if limit <= 0 || len(out) < limit {
		older, err := readDeadLetterTail(s.rotatedDeadLetterPath())
		if err != nil {
			return nil, err
		}
		out = append(older, out...)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func readDeadLetterTail(path string) ([]DeadLetter, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	offset := int64(0)
	if size > deadLetterTailScan {
		offset = size - deadLetterTailScan
	}
	raw := make([]byte, size-offset)
	if _, err := f.ReadAt(raw, offset); err != nil && err != io.EOF {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if offset > 0 && len(lines) > 0 {
		// The first line was cut in half by the offset.
		lines = lines[1:]
	}
	var out []DeadLetter
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var dl DeadLetter
		if err := json.Unmarshal([]byte(line), &dl); err != nil {
			continue
		}
		out = append(out, dl)
	}
	return out, nil
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:]))
}

func newSecret() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return "cvwh_" + hex.EncodeToString(b[:])
}
