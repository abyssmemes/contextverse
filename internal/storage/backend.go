package storage

import (
	"context"
	"errors"
	"fmt"
)

// Version is an opaque concurrency token for a blob or scope head.
// Empty Version means "object does not exist yet" (create-if-absent).
type Version string

// Entry is a listed object.
//
// Size is what the object holds, in bytes, as the backend knows it. It exists
// because quota accounting used to list the backend and then stat the local
// working-tree mirror for sizes — which works on the one machine that wrote the
// file and nowhere else. With s3 or sql the mirror is per-replica, so every
// other replica read the space as smaller than it is and let it grow past its
// limit; the documented stateless HA is exactly that arrangement.
//
// A backend that genuinely cannot answer cheaply may leave it zero, and callers
// treat zero as "unknown" rather than "empty".
type Entry struct {
	Path    string
	Version Version
	Size    int64
}

// Common errors.
var (
	ErrNotFound        = errors.New("storage: not found")
	ErrConflict        = errors.New("storage: version conflict")
	ErrNotSupported    = errors.New("storage: not supported")
	ErrInvalidArgument = errors.New("storage: invalid argument")
)

// Backend is the narrow pluggable store: blobs + optimistic CAS + scope heads.
// All versioning/history/ACL semantics live above this interface (see History).
// SetHead is required so core can CAS-advance scope markers; drivers must not
// invent merge/diff/ACL behavior.
type Backend interface {
	// Name returns a stable driver id (local|git|…).
	Name() string

	Get(ctx context.Context, path string) (data []byte, version Version, err error)
	List(ctx context.Context, prefix string) ([]Entry, error)
	Put(ctx context.Context, path string, data []byte, expected Version) (Version, error)
	Delete(ctx context.Context, path string, expected Version) error

	// Head returns the version marker for a scope (e.g. "space" or "projects/foo").
	Head(ctx context.Context, scope string) (Version, error)
	// SetHead updates the scope marker with CAS (expected empty = create).
	SetHead(ctx context.Context, scope string, expected, next Version) error
}

// ConflictError wraps ErrConflict with detail.
type ConflictError struct {
	Path     string
	Expected Version
	Actual   Version
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("storage conflict on %q: expected %q got %q", e.Path, e.Expected, e.Actual)
}

func (e *ConflictError) Is(target error) bool {
	return target == ErrConflict
}
