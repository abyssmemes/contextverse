package webhooks

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	body := []byte(`{"type":"space.push"}`)
	sig := Sign("secret", body)
	if !Verify("secret", sig, body) {
		t.Fatal("verify failed")
	}
	if Verify("other", sig, body) {
		t.Fatal("expected mismatch")
	}
}

func TestDeliverAndDeadLetter(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got []byte
	var gotSig string
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append([]byte(nil), raw...)
		gotSig = r.Header.Get("X-ContextVerse-Signature")
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer okSrv.Close()

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer failSrv.Close()

	ok, err := store.Upsert(Hook{URL: okSrv.URL, Events: []string{"space.push"}, Secret: "s3cret", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fail, err := store.Upsert(Hook{URL: failSrv.URL, Events: []string{"*"}, Secret: "x", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	d := NewDispatcher(store)
	d.Client.Timeout = 2 * time.Second
	evt := Event{Type: "space.push", Space: "team", Actor: "alice", Data: map[string]any{"ops": 1}}
	if err := d.postOnce(ok, evt); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(got) == 0 || !Verify("s3cret", gotSig, got) {
		t.Fatalf("delivery/signature: %s %s", gotSig, got)
	}
	mu.Unlock()

	if err := d.postOnce(fail, evt); err == nil {
		t.Fatal("expected fail")
	}
	_ = store.appendDeadLetter(DeadLetter{
		HookID: fail.ID, URL: fail.URL, Event: evt, Error: "status 500",
		FailedAt: time.Now().UTC(), Attempts: 1,
	})
	dl, err := store.ListDeadLetter(10)
	if err != nil || len(dl) == 0 {
		t.Fatalf("dead letter: %v %v", dl, err)
	}
}
