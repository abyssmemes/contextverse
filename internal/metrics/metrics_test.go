package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestWritePrometheus(t *testing.T) {
	r := New()
	r.HTTPRequests.Inc("GET", "200")
	r.HTTPDuration.Observe(0.012)
	r.RateLimited.Inc()
	r.SSEClients.Store(2)

	var b strings.Builder
	if err := r.WritePrometheus(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"contextd_up 1",
		`contextd_http_requests_total{method="GET",code="200"} 1`,
		"contextd_rate_limit_rejected_total 1",
		"contextd_sse_clients 2",
		"contextd_http_request_duration_seconds_count 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	_ = time.Now()
}
