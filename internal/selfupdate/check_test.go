package selfupdate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// A version notice is easy to write and easy to make hated. These are mostly
// about the silence: when it must say nothing, and that it says a given thing
// exactly once.

func checker(t *testing.T, current, latest string) *Checker {
	t.Helper()
	return &Checker{
		Current:  current,
		CacheDir: t.TempDir(),
		Now:      func() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) },
		Fetch:    func(context.Context) (string, error) { return latest, nil },
	}
}

// warm runs one Notice and waits for the background refresh to record want,
// which is what a second run of the command would see.
//
// Waiting for a specific value rather than for "anything recorded": after the
// first warm there is already a timestamp, so a weaker condition returns before
// the second refresh has landed and the test reads the previous answer.
func warm(t *testing.T, c *Checker, want string) {
	t.Helper()
	_ = c.Notice(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := c.load(); st.LatestSeen == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, _ := c.load()
	t.Fatalf("the background refresh recorded %q, want %q", st.LatestSeen, want)
}

// The first run says nothing: somebody who has just installed contextd does not
// need to be told about a release, and there is no cached answer yet anyway.
func TestTheFirstRunIsSilent(t *testing.T) {
	c := checker(t, "0.7.0", "0.9.0")
	if notice := c.Notice(context.Background()); notice != "" {
		t.Errorf("the first run spoke: %q", notice)
	}
	// Notice leaves a refresh running behind it. Let it finish before the test
	// ends, or it writes its state into a directory the framework is removing —
	// which Windows reports as "the directory is not empty" and Unix hides.
	waitForOneAsk(t, c)
}

func TestASecondRunReportsANewerRelease(t *testing.T) {
	c := checker(t, "0.7.0", "0.9.0")
	warm(t, c, "0.9.0")

	notice := c.Notice(context.Background())
	if !strings.Contains(notice, "0.9.0") || !strings.Contains(notice, "0.7.0") {
		t.Fatalf("notice = %q; want both versions named", notice)
	}
}

// Being told the same thing every day is what makes people turn it off, and
// then they never hear about the release that matters.
func TestTheSameVersionIsAnnouncedOnce(t *testing.T) {
	c := checker(t, "0.7.0", "0.9.0")
	warm(t, c, "0.9.0")

	if first := c.Notice(context.Background()); first == "" {
		t.Fatal("nothing was said at all")
	}
	for i := 0; i < 5; i++ {
		if again := c.Notice(context.Background()); again != "" {
			t.Fatalf("said it again on run %d: %q", i+2, again)
		}
	}
}

// A newer release after one has already been announced is news again.
func TestANewerReleaseIsAnnouncedAgain(t *testing.T) {
	c := checker(t, "0.7.0", "0.9.0")
	warm(t, c, "0.9.0")
	if c.Notice(context.Background()) == "" {
		t.Fatal("the first release was not announced")
	}

	// A day later, upstream has moved again.
	c.Now = func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) }
	c.Fetch = func(context.Context) (string, error) { return "1.0.0", nil }
	warm(t, c, "1.0.0")

	if notice := c.Notice(context.Background()); !strings.Contains(notice, "1.0.0") {
		t.Errorf("notice = %q; want the new release", notice)
	}
}

func TestNothingIsSaidWhenUpToDateOrAhead(t *testing.T) {
	for _, tc := range []struct{ current, latest string }{
		{"0.9.0", "0.9.0"},
		{"1.0.0", "0.9.0"}, // a local build ahead of the feed
	} {
		c := checker(t, tc.current, tc.latest)
		warm(t, c, tc.latest)
		if notice := c.Notice(context.Background()); notice != "" {
			t.Errorf("current %s vs latest %s: said %q", tc.current, tc.latest, notice)
		}
	}
}

// Telling a contributor their working tree is out of date is noise, and it is
// the version they see most often. A dev build does not even ask upstream: there
// is no answer that would change what it does.
func TestADevelopmentBuildIsSilentAndDoesNotAsk(t *testing.T) {
	var asked bool
	var mu sync.Mutex
	c := checker(t, "0.0.0-dev", "9.9.9")
	c.Fetch = func(context.Context) (string, error) {
		mu.Lock()
		asked = true
		mu.Unlock()
		return "9.9.9", nil
	}

	for i := 0; i < 3; i++ {
		if notice := c.Notice(context.Background()); notice != "" {
			t.Fatalf("a dev build was nagged: %q", notice)
		}
	}
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if asked {
		t.Error("a dev build went to the network for an answer it cannot use")
	}
}

// A release candidate is not a release either.
func TestAPrereleaseIsSilent(t *testing.T) {
	for _, v := range []string{"1.0.0-rc1", "0.9.0-beta.2", "1.2.3+build.5"} {
		c := checker(t, v, "9.9.9")
		if notice := c.Notice(context.Background()); notice != "" {
			t.Errorf("%s was nagged: %q", v, notice)
		}
	}
}

func TestTheEnvironmentSwitchSilencesEverything(t *testing.T) {
	c := checker(t, "0.7.0", "0.9.0")
	warm(t, c, "0.9.0")
	t.Setenv(EnvDisable, "1")

	if notice := c.Notice(context.Background()); notice != "" {
		t.Errorf("%s was ignored: %q", EnvDisable, notice)
	}
}

// A command must never wait for somebody else's server. The check reads the
// cache and refreshes behind it.
func TestNoticeDoesNotWaitForTheNetwork(t *testing.T) {
	release := make(chan struct{})
	c := checker(t, "0.7.0", "0.9.0")
	c.Fetch = func(ctx context.Context) (string, error) {
		<-release // never answers until the test says so
		return "0.9.0", nil
	}

	done := make(chan struct{})
	go func() { _ = c.Notice(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Notice blocked on the fetch")
	}

	// Let the refresh finish before the temp directory is cleaned up, or it
	// writes its state into a directory the test framework is removing.
	close(release)
	warmDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(warmDeadline) {
		if st, _ := c.load(); !st.LastCheck.IsZero() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the released fetch never recorded anything")
}

// An offline machine must not ask again on every single command. The timestamp
// moves whether or not the fetch worked.
func TestAFailedCheckStillCountsAsAsking(t *testing.T) {
	var asks int
	var mu sync.Mutex
	c := checker(t, "0.7.0", "")
	c.Fetch = func(context.Context) (string, error) {
		mu.Lock()
		asks++
		mu.Unlock()
		return "", errors.New("no network")
	}

	waitForOneAsk(t, c)
	for i := 0; i < 5; i++ {
		_ = c.Notice(context.Background())
	}
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if asks != 1 {
		t.Errorf("asked %d times after a failure; an offline machine would retry on every command", asks)
	}
}

// Between checks the cached answer is used and upstream is left alone.
func TestUpstreamIsAskedAtMostOncePerDay(t *testing.T) {
	var asks int
	var mu sync.Mutex
	c := checker(t, "0.7.0", "0.9.0")
	c.Fetch = func(context.Context) (string, error) {
		mu.Lock()
		asks++
		mu.Unlock()
		return "0.9.0", nil
	}

	warm(t, c, "0.9.0")
	for i := 0; i < 10; i++ {
		_ = c.Notice(context.Background())
	}
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	got := asks
	mu.Unlock()
	if got != 1 {
		t.Errorf("asked upstream %d times within the window, want 1", got)
	}

	// A day later it is allowed to ask again.
	c.Now = func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) }
	_ = c.Notice(context.Background())
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if asks != 2 {
		t.Errorf("asked %d times in total; the window never reopened", asks)
	}
}

// waitForOneAsk waits for a refresh that records no version, only that it tried.
func waitForOneAsk(t *testing.T, c *Checker) {
	t.Helper()
	_ = c.Notice(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := c.load(); !st.LastCheck.IsZero() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the failed check was never recorded")
}

func TestVersionParsing(t *testing.T) {
	for _, tc := range []struct {
		in    string
		ok    bool
		major int
	}{
		{"1.2.3", true, 1},
		{"v1.2.3", true, 1},
		{"v10.0.1", true, 10},
		{"1.2.3-rc1", true, 1},
		{"1.2.3+build.5", true, 1},
		{"0.0.0-dev", true, 0},
		{"1.2", false, 0},
		{"", false, 0},
		{"latest", false, 0},
		{"1.2.x", false, 0},
	} {
		got, ok := parse(tc.in)
		if ok != tc.ok {
			t.Errorf("parse(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got.major != tc.major {
			t.Errorf("parse(%q).major = %d, want %d", tc.in, got.major, tc.major)
		}
	}
}

func TestOrdering(t *testing.T) {
	for _, tc := range []struct {
		newer, older string
		want         bool
	}{
		{"1.0.0", "0.9.9", true},
		{"0.10.0", "0.9.0", true}, // not string order
		{"0.9.10", "0.9.9", true},
		{"0.9.0", "0.9.0", false},
		{"0.9.0", "1.0.0", false},
	} {
		a, _ := parse(tc.newer)
		b, _ := parse(tc.older)
		if got := a.newerThan(b); got != tc.want {
			t.Errorf("%s newerThan %s = %v, want %v", tc.newer, tc.older, got, tc.want)
		}
	}
}

// parse and parseRelease answer different questions: ordering may ignore a
// suffix, deciding whether to speak at all may not.
func TestOnlyPlainReleasesCount(t *testing.T) {
	for _, tc := range []struct {
		in      string
		release bool
	}{
		{"1.2.3", true},
		{"v1.2.3", true},
		{"1.2.3-rc1", false},
		{"0.0.0-dev", false},
		{"1.2.3+build.5", false},
		{"nonsense", false},
	} {
		if _, ok := parseRelease(tc.in); ok != tc.release {
			t.Errorf("parseRelease(%q) = %v, want %v", tc.in, ok, tc.release)
		}
	}
}
