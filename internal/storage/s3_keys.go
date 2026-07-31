package storage

import (
	"fmt"
	"strings"
)

// How an object's path becomes an S3 key, and why it changed.
//
// # The old scheme was too narrow and too opaque
//
// The key was contentVersion(path) — SHA-256 truncated to eight bytes. Sixty-four
// bits for a value that decides which file you are reading. Local used the full
// digest for the same job; only S3 was cut short, and nothing recorded why.
// Sixty-four bits is inside reach: a collision needs about 2^32 candidate paths
// to find by chance, and far less to construct deliberately. Two different paths
// landing on one key means one file silently overwrites the other, with no error
// anywhere.
//
// It was also unreadable. Because the key said nothing about the path, listing
// the space meant downloading every object in the bucket to read the path back
// out of its body — one GET per file, with the whole body on the wire, on every
// tree, every changes and every quota check. That is a bill and a wait for an
// answer S3 already had.
//
// # The new scheme is the path
//
// Keys are the path itself, escaped where S3 or a human would trip over it.
// Collisions become impossible rather than unlikely, listing needs no request
// beyond the listing, and the bucket is legible to whoever has to look at it at
// three in the morning.
//
// # Compatibility
//
// Buckets written by the old scheme still exist, so a read falls back to the old
// key, and a write to a path that still lives there moves it. A bucket converges
// as it is used, and a listing pays the old cost only for the objects that have
// not been touched yet.

const (
	s3ObjectsPrefix = "objects/"
	s3ObjectSuffix  = ".json"
	// s3MaxKeyLen is S3's own limit on a key, in bytes.
	s3MaxKeyLen = 1024
)

// objectKey returns the key for a path under the current scheme.
func (s *S3) objectKey(path string) string {
	return s.prefix + s3ObjectsPrefix + escapeKeySegment(sanitizePath(path)) + s3ObjectSuffix
}

// legacyObjectKey returns the key the old truncated-hash scheme would have used.
// Read-only: nothing new is ever written here.
func (s *S3) legacyObjectKey(path string) string {
	sum := contentVersion([]byte(sanitizePath(path)))
	return s.prefix + s3ObjectsPrefix + string(sum) + s3ObjectSuffix
}

// pathFromKey recovers the logical path from a key written under the current
// scheme, reporting false for anything else — including a legacy hashed key,
// which carries no path to recover.
func (s *S3) pathFromKey(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, s.prefix+s3ObjectsPrefix)
	if !ok {
		return "", false
	}
	rest, ok = strings.CutSuffix(rest, s3ObjectSuffix)
	if !ok || rest == "" {
		return "", false
	}
	path, err := unescapeKeySegment(rest)
	if err != nil {
		return "", false
	}
	// A legacy key is sixteen hex characters and decodes to itself. Treating one
	// as a path would invent a file named after a hash.
	if isLegacyHashKey(rest) {
		return "", false
	}
	return path, true
}

// isLegacyHashKey reports whether a key body looks like the old scheme's
// sixteen hex characters.
func isLegacyHashKey(s string) bool {
	if len(s) != 16 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// checkKeyLength refuses a path whose key would exceed what S3 accepts, rather
// than letting the store fail somewhere less obvious.
func (s *S3) checkKeyLength(path string) error {
	if k := s.objectKey(path); len(k) > s3MaxKeyLen {
		return fmt.Errorf("%w: path is too long for this bucket (key would be %d bytes, limit %d)",
			ErrInvalidArgument, len(k), s3MaxKeyLen)
	}
	return nil
}

// escapeKeySegment percent-encodes the few bytes that make an S3 key awkward,
// and nothing else.
//
// Deliberately not url.PathEscape, which also escapes characters that are
// perfectly good in a key and would leave the bucket full of %2F where a slash
// belongs. Slashes are kept: they are what make the listing look like the space
// it holds.
func escapeKeySegment(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for i := 0; i < len(path); i++ {
		c := path[i]
		if needsKeyEscape(c) {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func needsKeyEscape(c byte) bool {
	switch {
	case c == '%': // the escape character itself, or decoding is ambiguous
		return true
	case c < 0x20 || c == 0x7f: // control bytes
		return true
	case c == '\\', c == '{', c == '}', c == '^', c == '`', c == '"', c == '<', c == '>', c == '|':
		// Characters S3's own guidance calls out as needing special handling.
		return true
	default:
		return false
	}
}

func unescapeKeySegment(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", fmt.Errorf("truncated escape in key")
		}
		hi, err := hexVal(s[i+1])
		if err != nil {
			return "", err
		}
		lo, err := hexVal(s[i+2])
		if err != nil {
			return "", err
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), nil
}

func hexVal(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	}
	return 0, fmt.Errorf("bad escape digit %q", c)
}
