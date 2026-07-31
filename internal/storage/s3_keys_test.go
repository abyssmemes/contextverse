package storage

import (
	"strings"
	"testing"
)

// The old key was SHA-256 cut to eight bytes — sixty-four bits deciding which
// file you are reading, where Local used the full digest for the same job. A
// collision means one file silently overwriting another with no error anywhere,
// and sixty-four bits is well inside reach: about 2^32 candidates by chance, and
// far fewer to construct on purpose.
//
// Keys are the path now, so a collision is not unlikely but impossible. These
// check that the mapping is faithful in both directions, that the old keys are
// still recognised, and that the two schemes cannot be confused for each other.

func s3For(prefix string) *S3 {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &S3{bucket: "b", prefix: prefix}
}

func TestDistinctPathsGetDistinctKeys(t *testing.T) {
	s := s3For("spaces/team")
	paths := []string{
		"notes.md",
		"Notes.md",
		"team/notes.md",
		"team/notes.md.bak",
		"a/b/c.md",
		"a/b%2Fc.md",
		"identity/me.md",
		strings.Repeat("deep/", 20) + "x.md",
	}
	seen := map[string]string{}
	for _, p := range paths {
		k := s.objectKey(p)
		if prev, dup := seen[k]; dup {
			t.Fatalf("%q and %q share the key %q", prev, p, k)
		}
		seen[k] = p
	}
}

func TestAKeyRoundTripsBackToItsPath(t *testing.T) {
	s := s3For("spaces/team")
	for _, p := range []string{
		"notes.md",
		"team/principles.md",
		"projects/example/services/README.md",
		"odd name with spaces.md",
		"unicode-Привет-文件.md",
		"percent%sign.md",
		"back\\slash.md",
		"brace{}caret^.md",
	} {
		key := s.objectKey(p)
		got, ok := s.pathFromKey(key)
		if !ok {
			t.Errorf("%q: key %q did not decode", p, key)
			continue
		}
		if got != p {
			t.Errorf("%q round-tripped to %q via %q", p, got, key)
		}
	}
}

// A legacy key carries no path, so it must not be mistaken for one — otherwise
// the listing invents a file named after a hash.
func TestALegacyKeyIsNotReadAsAPath(t *testing.T) {
	s := s3For("spaces/team")
	legacy := s.legacyObjectKey("notes.md")
	if _, ok := s.pathFromKey(legacy); ok {
		t.Errorf("legacy key %q was decoded as a path", legacy)
	}
	if !strings.HasSuffix(legacy, ".json") {
		t.Errorf("legacy key shape changed: %q", legacy)
	}
}

// The legacy scheme must keep producing exactly what it used to, or reads
// against an existing bucket miss.
func TestTheLegacyKeyIsUnchanged(t *testing.T) {
	s := s3For("spaces/team")
	want := "spaces/team/objects/" + string(contentVersion([]byte("notes.md"))) + ".json"
	if got := s.legacyObjectKey("notes.md"); got != want {
		t.Errorf("legacy key = %q, want %q", got, want)
	}
}

func TestKeysFromAnotherPrefixAreIgnored(t *testing.T) {
	s := s3For("spaces/team")
	for _, key := range []string{
		"spaces/other/objects/notes.md.json",
		"spaces/team/heads/abc.head",
		"spaces/team/objects/notes.md", // no suffix
		"unrelated",
		"",
	} {
		if p, ok := s.pathFromKey(key); ok {
			t.Errorf("%q was decoded as path %q", key, p)
		}
	}
}

// A path long enough to push the key past what S3 accepts must be refused here
// rather than failing somewhere less legible.
func TestAnOverlongPathIsRefused(t *testing.T) {
	s := s3For("spaces/team")
	if err := s.checkKeyLength("notes.md"); err != nil {
		t.Fatalf("an ordinary path was refused: %v", err)
	}
	if err := s.checkKeyLength(strings.Repeat("x", s3MaxKeyLen)); err == nil {
		t.Error("a path whose key exceeds S3's limit was accepted")
	}
}

func TestEscapingIsReversible(t *testing.T) {
	for _, in := range []string{
		"plain",
		"with/slashes/kept",
		"percent%20literal",
		"control\x01byte",
		"quote\"brace{}",
		"",
	} {
		esc := escapeKeySegment(in)
		got, err := unescapeKeySegment(esc)
		if err != nil {
			t.Errorf("%q escaped to %q which will not decode: %v", in, esc, err)
			continue
		}
		if got != in {
			t.Errorf("%q -> %q -> %q", in, esc, got)
		}
	}
}

// Slashes survive escaping: they are what makes a bucket listing look like the
// space it holds, which is the difference between a legible store and a wall of
// hashes.
func TestSlashesAreNotEscaped(t *testing.T) {
	if got := escapeKeySegment("team/projects/notes.md"); got != "team/projects/notes.md" {
		t.Errorf("escaped to %q; slashes should be kept", got)
	}
}

func TestBadEscapesAreRejected(t *testing.T) {
	for _, in := range []string{"%", "%A", "%ZZ", "abc%G0"} {
		if _, err := unescapeKeySegment(in); err == nil {
			t.Errorf("%q decoded without complaint", in)
		}
	}
}
