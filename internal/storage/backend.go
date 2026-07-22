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
type Entry struct {
	Path    string
	Version Version
}

// Common errors.
var (
	ErrNotFound         = errors.New("storage: not found")
	ErrConflict         = errors.New("storage: version conflict")
	ErrNotSupported     = errors.New("storage: not supported")
	ErrInvalidArgument  = errors.New("storage: invalid argument")
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
