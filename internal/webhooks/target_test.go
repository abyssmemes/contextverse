package webhooks

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStrictPolicyRefusesInternalTargets(t *testing.T) {
	p := TargetPolicy{}
	refuse := []string{
		"http://127.0.0.1:4646/v1/jobs",
		"http://localhost:8081/admin",
		"http://10.0.0.5/hook",
		"http://192.168.1.10/hook",
		"http://172.16.4.4/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://[::1]:8080/hook",
		"http://[fd00::1]/hook",
		"http://100.100.0.1/hook",
		"file:///etc/passwd",
		"gopher://10.0.0.1:70/",
	}
	for _, raw := range refuse {
		if err := p.ValidateURL(raw); !errors.Is(err, ErrUnsafeTarget) {
			t.Errorf("ValidateURL(%q) must be refused, got %v", raw, err)
		}
	}
	if err := p.ValidateURL("https://hooks.example.com/inbox"); err != nil {
		t.Errorf("public https target must be allowed: %v", err)
	}
}

func TestMetadataStaysRefusedEvenWhenPrivateIsAllowed(t *testing.T) {
	p := TargetPolicy{AllowPrivate: true}
	if err := p.ValidateURL("http://127.0.0.1:9000/hook"); err != nil {
		t.Fatalf("self-hosted internal target must be allowed: %v", err)
	}
	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://169.254.170.2/v2/credentials",
		"http://metadata.google.internal/",
	} {
		if err := p.ValidateURL(raw); !errors.Is(err, ErrUnsafeTarget) {
			t.Errorf("%s must stay refused: %v", raw, err)
		}
	}
}

func TestStoreRefusesUnsafeHook(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Upsert(Hook{URL: "http://169.254.169.254/", Enabled: true}); !errors.Is(err, ErrUnsafeTarget) {
		t.Fatalf("store must refuse a metadata hook, got %v", err)
	}
	list, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("refused hook must not be persisted: %+v", list)
	}
}

// The socket check is what survives DNS rebinding: the name looked public when
// the hook was saved, and resolves to loopback at delivery time.
func TestDialRefusesInternalAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := TargetPolicy{}.Client(2 * time.Second)
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("a loopback destination must not be dialled under the strict policy")
	}
	if !strings.Contains(err.Error(), "unsafe webhook target") {
		t.Fatalf("unexpected dial error: %v", err)
	}

	if _, err := (TargetPolicy{AllowPrivate: true}).Client(2 * time.Second).Get(srv.URL); err != nil {
		t.Fatalf("allow-private client must reach loopback: %v", err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var hits int
	var mu sync.Mutex
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	res, err := (TargetPolicy{AllowPrivate: true}).Client(2 * time.Second).Get(redirector.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("redirect should be surfaced, not followed: %d", res.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("redirect target was contacted %d time(s)", hits)
	}
}

func TestQueueDropsInsteadOfGrowingUnbounded(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(st)
	var dropped int
	var mu sync.Mutex
	d.OnDropped = func(Event) {
		mu.Lock()
		dropped++
		mu.Unlock()
	}
	// Block the workers so the queue has to fill.
	release := make(chan struct{})
	d.queue = make(chan Event, 1)
	d.startPool.Do(func() {
		for i := 0; i < deliveryWorkers; i++ {
			go func() {
				for range d.queue {
					<-release
				}
			}()
		}
	})
	for i := 0; i < deliveryWorkers+deliveryQueueSize+64; i++ {
		d.Emit(Event{Type: "space.push"})
	}
	close(release)
	mu.Lock()
	defer mu.Unlock()
	if dropped == 0 {
		t.Fatal("a full queue must drop and report, not grow without bound")
	}
}
