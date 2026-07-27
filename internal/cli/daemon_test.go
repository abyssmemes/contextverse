package cli

import (
	"testing"
	"time"
)

// A fixed interval meant an unreachable server produced an identical failed
// request every minute forever. Backoff has to widen, cap, and — the part that
// is easy to get wrong — not punish a single dropped packet.
func TestBackoffWidensAndCaps(t *testing.T) {
	base := time.Minute

	if got := backoffFor(base, 0); got != base {
		t.Errorf("no failures = %v, want the base interval %v", got, base)
	}
	if got := backoffFor(base, 1); got != base {
		t.Errorf("first failure = %v, want %v — one dropped packet should retry normally", got, base)
	}
	if got := backoffFor(base, 2); got != 2*time.Minute {
		t.Errorf("second failure = %v, want 2m", got)
	}
	if got := backoffFor(base, 3); got != 4*time.Minute {
		t.Errorf("third failure = %v, want 4m", got)
	}

	// Monotonic, and never past the cap however long the outage runs.
	prev := time.Duration(0)
	for f := 1; f <= 100; f++ {
		got := backoffFor(base, f)
		if got < prev {
			t.Fatalf("failure %d backed off to %v, less than the previous %v", f, got, prev)
		}
		if got > daemonMaxBackoff {
			t.Fatalf("failure %d = %v, past the %v cap", f, got, daemonMaxBackoff)
		}
		prev = got
	}
	if backoffFor(base, 100) != daemonMaxBackoff {
		t.Errorf("a long outage should settle at the cap, got %v", backoffFor(base, 100))
	}
}

// A sub-minute interval must still back off rather than staying tight.
func TestBackoffRespectsShortIntervals(t *testing.T) {
	base := 5 * time.Second
	if got := backoffFor(base, 4); got != 40*time.Second {
		t.Errorf("got %v, want 40s (5s doubled three times)", got)
	}
	if got := backoffFor(base, 60); got != daemonMaxBackoff {
		t.Errorf("got %v, want the cap", got)
	}
}
