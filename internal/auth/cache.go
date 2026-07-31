package auth

import (
	"crypto/subtle"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Authentication used to cost a directory walk. Every bearer token presented to
// the API made the store read `tokens/`, open and JSON-decode each file until
// one matched, and then read and YAML-decode `users.yaml` — under the read lock,
// so writers queued behind it. At a few thousand issued tokens that is a few
// thousand file reads to answer one request, and it grows with the number of
// credentials the server has ever issued.
//
// The caches below remove the reads without changing what an answer means.
// That last part is the whole difficulty: Authenticate re-read both files on
// purpose, so that demoting or disabling someone took effect on their live
// tokens immediately rather than whenever they next logged in. A cache that
// serves a stale grant is a security bug, not a slow path.
//
// So neither cache is time-based. Both are validated against the filesystem on
// every call, with one cheap syscall standing in for the reads:
//
//   - Tokens: one ReadDir. A token file is immutable once written — issuing
//     creates a file under a fresh random id, revoking removes one, nothing
//     edits in place — so the set of names is a complete description of the
//     token store. Names already parsed are not read again; names that vanished
//     are dropped, which is what makes revocation take effect. Editing a token
//     file's contents by hand is not a supported operation and is the one
//     change the names would not show; the write paths drop the cache anyway.
//   - Users: one Stat. users.yaml is rewritten whole (tmp + rename), so size and
//     modification time move whenever grants change. saveUsers also drops the
//     entry outright, which covers writes made through this process regardless
//     of timestamp resolution.
//
// Both therefore stay correct when the files are changed by something other
// than this process — an operator editing users.yaml by hand, or a second
// contextd sharing the data directory.

type tokenCache struct {
	mu sync.Mutex
	// byFile is the parsed record for each token file we have already read.
	byFile map[string]TokenRecord
	// byHash points a token hash at its file, so a presented token is one map
	// lookup rather than a scan.
	byHash map[string]string

	// scanned records the directory state the maps were built from. A POSIX
	// directory's modification time moves whenever an entry is added or
	// removed, which is the only way the token store ever changes, so an
	// unchanged stamp means the maps are still a complete description.
	scanned bool
	dirMod  time.Time
	// uncertain marks a scan taken within a second of the directory's own
	// timestamp. On a filesystem that stores that timestamp coarsely, a second
	// change in the same tick would carry the same stamp and go unseen, so the
	// next call rescans rather than trusting it.
	uncertain bool
}

type usersCache struct {
	mu      sync.Mutex
	loaded  bool
	file    usersFile
	size    int64
	modTime time.Time
}

// tokenByHash returns the record whose hash matches, refreshing from disk first.
//
// The comparison stays constant-time. A map lookup finds the candidate; the
// hash is then compared byte by byte in constant time, so a token that shares a
// prefix with a real one is not distinguishable from one that does not.
func (s *Store) tokenByHash(hash string) (TokenRecord, bool) {
	c := &s.tokens
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.refresh(s.tokensDir()); err != nil {
		return TokenRecord{}, false
	}

	name, ok := c.byHash[hash]
	if !ok {
		return TokenRecord{}, false
	}
	rec := c.byFile[name]
	if !constantTimeEqual(rec.Hash, hash) {
		return TokenRecord{}, false
	}
	return rec, true
}

// refresh brings the maps in line with the directory, doing as little as it can.
//
// The steady state is one Stat. Only a directory whose timestamp has moved is
// read, and only files not already parsed are opened, so the cost tracks how
// often tokens are issued or revoked rather than how many exist.
func (c *tokenCache) refresh(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if c.scanned && !c.uncertain && st.ModTime().Equal(c.dirMod) {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	present := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		present[name] = struct{}{}
		if _, known := c.byFile[name]; known {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var rec TokenRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if c.byFile == nil {
			c.byFile = map[string]TokenRecord{}
			c.byHash = map[string]string{}
		}
		c.byFile[name] = rec
		c.byHash[rec.Hash] = name
	}

	// Forget revoked tokens. Keeping one would keep a withdrawn credential
	// working, which is the one failure this cache must not have.
	for name, rec := range c.byFile {
		if _, ok := present[name]; !ok {
			delete(c.byFile, name)
			delete(c.byHash, rec.Hash)
		}
	}

	c.scanned = true
	c.dirMod = st.ModTime()
	c.uncertain = time.Since(st.ModTime()) < time.Second
	return nil
}

// dropTokens clears the token cache. Only needed for the paths that remove
// files while holding the store lock; ReadDir would notice anyway, but saying
// so keeps the two in step within a single request.
func (s *Store) dropTokens() {
	s.tokens.mu.Lock()
	s.tokens.byFile = nil
	s.tokens.byHash = nil
	s.tokens.scanned = false
	s.tokens.mu.Unlock()
}

// usersNow returns the current users file, re-reading it only when the file on
// disk has moved.
func (s *Store) usersNow() (usersFile, error) {
	c := &s.usersCached
	c.mu.Lock()
	defer c.mu.Unlock()

	path := s.usersPath()
	st, err := os.Stat(path)
	if err != nil {
		return usersFile{}, err
	}
	if c.loaded && c.size == st.Size() && c.modTime.Equal(st.ModTime()) {
		return c.file, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return usersFile{}, err
	}
	var f usersFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return usersFile{}, err
	}
	c.file, c.size, c.modTime, c.loaded = f, st.Size(), st.ModTime(), true
	return f, nil
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// dropUsers forgets the cached grants. Called on every write to users.yaml, so
// a demotion applies within the same process even if the filesystem's timestamp
// resolution would not have shown the change.
func (s *Store) dropUsers() {
	s.usersCached.mu.Lock()
	s.usersCached.loaded = false
	s.usersCached.mu.Unlock()
}
