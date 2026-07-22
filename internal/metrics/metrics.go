package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Registry is a tiny Prometheus text exporter (no client_golang dependency).
type Registry struct {
	start time.Time

	HTTPRequests   *CounterVec // method, code
	HTTPDuration   *Histogram  // seconds
	RateLimited    *Counter
	PushTotal      *Counter
	WebhookFired   *Counter
	WebhookFailed  *Counter
	AuditEntries   *Counter
	SSEEvents      *Counter
	SSEClients     atomic.Int64
}

// New builds the default contextd metric set.
func New() *Registry {
	return &Registry{
		start:        time.Now().UTC(),
		HTTPRequests: NewCounterVec("contextd_http_requests_total", "Total HTTP requests", []string{"method", "code"}),
		HTTPDuration: NewHistogram("contextd_http_request_duration_seconds", "HTTP request latency seconds",
			[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}),
		RateLimited:   NewCounter("contextd_rate_limit_rejected_total", "Requests rejected by rate limit"),
		PushTotal:     NewCounter("contextd_push_total", "Successful space.push operations"),
		WebhookFired:  NewCounter("contextd_webhook_fired_total", "Webhook deliveries succeeded"),
		WebhookFailed: NewCounter("contextd_webhook_failed_total", "Webhook deliveries failed (dead-letter)"),
		AuditEntries:  NewCounter("contextd_audit_entries_total", "Audit log entries appended"),
		SSEEvents:     NewCounter("contextd_sse_events_total", "Events published to the SSE hub"),
	}
}

// WritePrometheus writes exposition format to w.
func (r *Registry) WritePrometheus(w io.Writer) error {
	if r == nil {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# HELP contextd_up 1 if the process is running\n# TYPE contextd_up gauge\ncontextd_up 1\n")
	fmt.Fprintf(&b, "# HELP contextd_start_time_seconds Process start unix time\n# TYPE contextd_start_time_seconds gauge\ncontextd_start_time_seconds %d\n", r.start.Unix())
	fmt.Fprintf(&b, "# HELP contextd_sse_clients Current SSE subscribers\n# TYPE contextd_sse_clients gauge\ncontextd_sse_clients %d\n", r.SSEClients.Load())
	r.HTTPRequests.write(&b)
	r.HTTPDuration.write(&b)
	r.RateLimited.write(&b)
	r.PushTotal.write(&b)
	r.WebhookFired.write(&b)
	r.WebhookFailed.write(&b)
	r.AuditEntries.write(&b)
	r.SSEEvents.write(&b)
	_, err := io.WriteString(w, b.String())
	return err
}

// Counter is a simple atomic counter.
type Counter struct {
	name, help string
	v          atomic.Uint64
}

func NewCounter(name, help string) *Counter {
	return &Counter{name: name, help: help}
}

func (c *Counter) Inc() {
	if c != nil {
		c.v.Add(1)
	}
}

func (c *Counter) Add(n uint64) {
	if c != nil {
		c.v.Add(n)
	}
}

func (c *Counter) write(b *strings.Builder) {
	if c == nil {
		return
	}
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", c.name, c.help, c.name, c.name, c.v.Load())
}

// CounterVec is a labeled counter (fixed label names).
type CounterVec struct {
	name, help string
	labels     []string
	mu         sync.Mutex
	m          map[string]*atomic.Uint64
}

func NewCounterVec(name, help string, labels []string) *CounterVec {
	return &CounterVec{name: name, help: help, labels: labels, m: map[string]*atomic.Uint64{}}
}

func (c *CounterVec) Inc(labelValues ...string) {
	if c == nil {
		return
	}
	key := strings.Join(labelValues, "\x00")
	c.mu.Lock()
	a, ok := c.m[key]
	if !ok {
		a = &atomic.Uint64{}
		c.m[key] = a
	}
	c.mu.Unlock()
	a.Add(1)
}

func (c *CounterVec) write(b *strings.Builder) {
	if c == nil {
		return
	}
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
	c.mu.Lock()
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals := strings.Split(k, "\x00")
		parts := make([]string, 0, len(c.labels))
		for i, lab := range c.labels {
			v := ""
			if i < len(vals) {
				v = vals[i]
			}
			parts = append(parts, fmt.Sprintf(`%s="%s"`, lab, escapeLabel(v)))
		}
		fmt.Fprintf(b, "%s{%s} %d\n", c.name, strings.Join(parts, ","), c.m[k].Load())
	}
	c.mu.Unlock()
}

func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// Histogram is a fixed-bucket latency histogram.
type Histogram struct {
	name, help string
	bounds     []float64
	mu         sync.Mutex
	counts     []uint64
	sum        float64
	n          uint64
}

func NewHistogram(name, help string, bounds []float64) *Histogram {
	return &Histogram{name: name, help: help, bounds: bounds, counts: make([]uint64, len(bounds)+1)}
}

func (h *Histogram) Observe(seconds float64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sum += seconds
	h.n++
	i := 0
	for ; i < len(h.bounds); i++ {
		if seconds <= h.bounds[i] {
			break
		}
	}
	h.counts[i]++
}

func (h *Histogram) write(b *strings.Builder) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	var cum uint64
	for i, bound := range h.bounds {
		cum += h.counts[i]
		fmt.Fprintf(b, `%s_bucket{le="%g"} %d`+"\n", h.name, bound, cum)
	}
	cum += h.counts[len(h.bounds)]
	fmt.Fprintf(b, `%s_bucket{le="+Inf"} %d`+"\n", h.name, cum)
	fmt.Fprintf(b, "%s_sum %g\n%s_count %d\n", h.name, h.sum, h.name, h.n)
}
