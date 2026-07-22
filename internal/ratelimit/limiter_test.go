package ratelimit

import "testing"

func TestAllowBurstThenLimit(t *testing.T) {
	l := New(Config{Enabled: true, RequestsPerMinute: 5, AuthPerMinute: 2})
	for i := 0; i < 5; i++ {
		ok, _, _, _, _ := l.Allow("u", false)
		if !ok {
			t.Fatalf("burst %d should allow", i)
		}
	}
	ok, _, rem, _, retry := l.Allow("u", false)
	if ok || rem != 0 || retry < 1 {
		t.Fatalf("expected limit ok=%v rem=%d retry=%d", ok, rem, retry)
	}
	// other key independent
	ok, _, _, _, _ = l.Allow("v", false)
	if !ok {
		t.Fatal("other key should allow")
	}
}

func TestDisabled(t *testing.T) {
	l := New(Config{Enabled: false, RequestsPerMinute: 1})
	for i := 0; i < 10; i++ {
		ok, _, _, _, _ := l.Allow("u", false)
		if !ok {
			t.Fatal("disabled must allow")
		}
	}
}
