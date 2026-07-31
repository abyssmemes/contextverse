// Package selfupdate tells someone a newer contextd exists, and otherwise says
// nothing at all.
//
// # The design problem is not the check, it is the noise
//
// A version notice is easy to write and easy to make hated. Three ways it goes
// wrong, in order of how much damage they do:
//
//  1. It corrupts something. `contextd mcp serve` speaks JSON-RPC over stdio; a
//     stray line there breaks the AI client's parser. `--json` output is read by
//     scripts. Anything printed to stdout can end up inside a pipe. So the
//     notice goes to stderr, never stdout, and whole commands are excluded
//     rather than trusted to be careful.
//  2. It costs time. A network call on the hot path makes every command wait for
//     someone else's server. So the check never blocks: a command uses whatever
//     is already in the cache and refreshes in the background for next time.
//  3. It repeats. Being told the same thing daily is what makes people reach for
//     the off switch, and then they never hear about the release that matters.
//     One notice per new version, and at most one check a day.
//
// The result is that a person sees a single line the first time a release
// appears, and scripts, CI and AI integrations see nothing ever.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// checkEvery bounds how often we ask. A day is short enough to hear about a
	// release the week it happens and long enough that nobody notices us asking.
	checkEvery = 24 * time.Hour
	// fetchTimeout bounds the background fetch. It is not on anyone's critical
	// path, but a goroutine that never ends is still a leak.
	fetchTimeout = 5 * time.Second
	// releasesURL is the source of truth for "what is the latest".
	releasesURL = "https://api.github.com/repos/orkcom-tech/contextverse/releases/latest"
	// EnvDisable turns the whole thing off.
	EnvDisable = "CONTEXTD_NO_UPDATE_CHECK"
)

// state is what we remember between runs.
type state struct {
	// LastCheck is when we last asked upstream, successful or not. Failures are
	// recorded too: an offline machine must not retry on every command.
	LastCheck time.Time `json:"last_check"`
	// LatestSeen is the newest version upstream reported.
	LatestSeen string `json:"latest_seen,omitempty"`
	// Announced is the version we have already told this person about, so the
	// same news is delivered once rather than daily.
	Announced string `json:"announced,omitempty"`
}

// Checker decides whether to say anything, and says it at most once.
type Checker struct {
	// Current is the running version.
	Current string
	// CacheDir holds the state file. Empty uses the user cache directory.
	CacheDir string
	// Now is overridable for tests.
	Now func() time.Time
	// Fetch returns the latest published version. Overridable for tests.
	Fetch func(ctx context.Context) (string, error)
	// Disabled skips everything, whatever else is true.
	Disabled bool
}

// Notice returns the line to show, or "" for silence.
//
// It never blocks on the network: the answer comes from the cache, and a stale
// cache is refreshed in the background for the next run. The first run after
// install therefore says nothing, which is correct — somebody who just
// downloaded contextd does not need to be told about a release.
func (c *Checker) Notice(ctx context.Context) string {
	if c.Disabled || os.Getenv(EnvDisable) != "" {
		return ""
	}
	cur, ok := parseRelease(c.Current)
	if !ok {
		// Anything that is not a plain release — 0.0.0-dev from a working tree,
		// a release candidate, a build stamped by hand — has no meaningful place
		// in the sequence. Telling a contributor their working tree is out of
		// date is noise, and it is the version they see most often.
		return ""
	}

	st, path := c.load()
	if c.now().Sub(st.LastCheck) > checkEvery {
		go c.refresh(path, st)
	}

	latest, ok := parse(st.LatestSeen)
	if !ok || !latest.newerThan(cur) {
		return ""
	}
	if st.Announced == st.LatestSeen {
		return "" // already said, once is enough
	}

	st.Announced = st.LatestSeen
	c.save(path, st)
	return fmt.Sprintf("A newer contextd is available: %s (you have %s). https://github.com/orkcom-tech/contextverse/releases/latest",
		st.LatestSeen, c.Current)
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// refresh asks upstream and records the answer. Runs detached from the command
// that started it, so its failure is nobody's problem.
func (c *Checker) refresh(path string, st state) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	fetch := c.Fetch
	if fetch == nil {
		fetch = fetchLatest
	}
	latest, err := fetch(ctx)
	// The timestamp moves either way. Recording only successes means an offline
	// machine asks again on every single command.
	st.LastCheck = c.now()
	if err == nil && latest != "" {
		st.LatestSeen = latest
	}
	c.save(path, st)
}

func (c *Checker) statePath() string {
	dir := c.CacheDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(base, "contextverse")
	}
	return filepath.Join(dir, "update-check.json")
}

func (c *Checker) load() (state, string) {
	path := c.statePath()
	if path == "" {
		return state{}, ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return state{}, path
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		return state{}, path
	}
	return st, path
}

// save is best-effort throughout. Nothing here is worth failing a command over,
// and the worst case of a lost write is asking again tomorrow.
func (c *Checker) save(path string, st state) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

func fetchLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release feed: HTTP %d", res.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	return strings.TrimSpace(body.TagName), nil
}

// semver is the subset needed to answer "is that one newer".
type semver struct{ major, minor, patch int }

// parseRelease accepts only a plain release: 1.2.3 or v1.2.3, and nothing with
// a prerelease or build suffix.
//
// Kept separate from parse because the two questions differ. Ordering can
// ignore a suffix; deciding whether to speak at all cannot, or 0.0.0-dev — the
// version every contributor runs — gets told it is nine releases behind.
func parseRelease(v string) (semver, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if strings.ContainsAny(trimmed, "-+") {
		return semver{}, false
	}
	return parse(v)
}

func parse(v string) (semver, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	// A prerelease or build suffix does not change the ordering question here.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var out semver
	for i, dst := range []*int{&out.major, &out.minor, &out.patch} {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return semver{}, false
		}
		*dst = n
	}
	return out, true
}

func (s semver) newerThan(other semver) bool {
	if s.major != other.major {
		return s.major > other.major
	}
	if s.minor != other.minor {
		return s.minor > other.minor
	}
	return s.patch > other.patch
}
