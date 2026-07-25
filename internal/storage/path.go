package storage

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrPathEscape means a caller-supplied path pointed outside its own space.
// Backends must reject such paths instead of normalizing them away: in a shared
// backend (S3/SQL with a per-space prefix, or a pooled server) a normalized
// "../other-space/notes.md" resolves to another tenant's object.
var ErrPathEscape = fmt.Errorf("%w: path escapes its space", ErrInvalidArgument)

// MaxPathLen bounds a single blob path.
const MaxPathLen = 1024

// CleanPath canonicalizes a blob path to a relative, slash-separated form and
// rejects anything that escapes the space root. The empty path is allowed and
// means "the whole space" (List prefixes, root scope).
func CleanPath(p string) (string, error) {
	if len(p) > MaxPathLen {
		return "", fmt.Errorf("%w: path longer than %d bytes", ErrInvalidArgument, MaxPathLen)
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: NUL byte in path", ErrInvalidArgument)
	}
	// Blob paths are slash-separated by contract. Treat a backslash as a
	// separator on every OS so the same path is judged identically wherever a
	// server, a sync client, or a shared backend sees it.
	q := strings.ReplaceAll(p, `\`, "/")
	// A Windows volume or UNC share must never survive into a relative path.
	if filepath.VolumeName(filepath.FromSlash(q)) != "" || len(q) > 1 && q[1] == ':' {
		return "", ErrPathEscape
	}
	q = strings.TrimPrefix(q, "/")
	if q == "" {
		return "", nil
	}
	q = path.Clean(q)
	switch {
	case q == ".":
		return "", nil
	case q == "..", strings.HasPrefix(q, "../"), strings.HasPrefix(q, "/"):
		return "", ErrPathEscape
	}
	return q, nil
}

// CleanFilePath is CleanPath for operations that need an actual object: unlike
// a List prefix, an empty path is an error.
func CleanFilePath(p string) (string, error) {
	clean, err := CleanPath(p)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidArgument)
	}
	return clean, nil
}

// ResolveUnder maps a caller-supplied relative path to an absolute path inside
// root, refusing traversal and symlinks that lead out of the tree.
func ResolveUnder(root, rel string) (string, error) {
	clean, err := CleanFilePath(rel)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(root, filepath.FromSlash(clean))
	if !withinRoot(root, abs) {
		return "", ErrPathEscape
	}
	// A symlink planted earlier (by a template, a sync, or a shared volume) can
	// point anywhere; compare the deepest existing ancestors, not the strings.
	realRoot := resolveExisting(root)
	realAbs := resolveExisting(abs)
	if !withinRoot(realRoot, realAbs) {
		return "", ErrPathEscape
	}
	return abs, nil
}

// withinRoot reports whether abs is root itself or below it.
func withinRoot(root, abs string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// resolveExisting resolves symlinks on the deepest existing ancestor of p and
// re-appends the not-yet-created remainder.
func resolveExisting(p string) string {
	remainder := ""
	cur := filepath.Clean(p)
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if remainder == "" {
				return real
			}
			return filepath.Join(real, remainder)
		} else if !os.IsNotExist(err) {
			return p
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		remainder = filepath.Join(filepath.Base(cur), remainder)
		cur = parent
	}
}

// MaxSpaceNameLen bounds a space name.
const MaxSpaceNameLen = 64

// ValidSpaceName guards names that become both directory components and shared
// backend key prefixes.
func ValidSpaceName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: space name is required", ErrInvalidArgument)
	case len(name) > MaxSpaceNameLen:
		return fmt.Errorf("%w: space name longer than %d bytes", ErrInvalidArgument, MaxSpaceNameLen)
	case name == ".", name == "..",
		strings.Contains(name, "/"), strings.Contains(name, `\`), strings.Contains(name, ".."):
		return ErrPathEscape
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("%w: space name must not start with a dot", ErrInvalidArgument)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("%w: space name may only contain letters, digits, '-', '_' and '.'", ErrInvalidArgument)
		}
	}
	return nil
}
