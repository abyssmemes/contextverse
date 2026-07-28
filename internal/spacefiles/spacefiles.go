// Package spacefiles answers one question — what is in this context space? —
// for every surface that has to display it.
//
// # Why this is its own package
//
// The CLI, the TUI and the web console each grew their own copy of this, and
// each copy listed the version log rather than the space. On a space created by
// the current build the two sets are identical, so the copies agreed and the
// duplication looked harmless. On a space carried across an upgrade the version
// log is empty and the directory is not, and then the surfaces contradicted one
// another in public: the Space tab counted eight documents while the Files tab
// said there were none, and `file list` printed "(no files)" over a directory
// the user was looking at in another window.
//
// Three fixes were written and the fourth copy was missed, which is the usual
// arithmetic of duplicated logic. There is one implementation now.
package spacefiles

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/orkcom-tech/contextverse/internal/storage"
)

// Entry is one file in the space.
type Entry struct {
	// Path is relative to the space root, slash-separated.
	Path string
	// Version is the current version, meaningful only when Tracked.
	Version storage.Version
	// Tracked reports whether the version log knows this file. An untracked
	// file is a normal state, not an error: it is a document that exists but
	// has no history yet, and it must still be listed, opened and edited.
	// Writing to it records the first version.
	Tracked bool
}

// Display renders the version the way every surface shows it: "v3" for a
// tracked file, empty for one the log has not seen.
func (e Entry) Display() string {
	if !e.Tracked {
		return ""
	}
	return storage.DisplayVersion(e.Version)
}

// skipDirs are never part of the context space itself.
var skipDirs = map[string]bool{
	".contextverse": true,
	".sync":         true,
	".git":          true,
	"node_modules":  true,
}

// List returns the union of the working tree and the version log, sorted by
// path.
//
// The union is the point. The tree alone misses a file that was deleted on disk
// but still has history worth restoring; the log alone misses everything on a
// space that predates it. Either source on its own produces a surface that
// disagrees with what the user can see.
//
// fl may be nil, in which case every entry is untracked.
func List(ctx context.Context, fl *storage.FileLog, root string) ([]Entry, error) {
	versions := map[string]storage.Version{}
	if fl != nil && fl.Backend != nil {
		entries, err := fl.Backend.List(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !isSpaceFile(e.Path) {
				continue
			}
			ver := e.Version
			// The log records the version at write time; LiveVersion accounts
			// for anything that has happened since.
			if lv, err := fl.LiveVersion(ctx, e.Path); err == nil {
				ver = lv
			}
			versions[e.Path] = ver
		}
	}

	seen := map[string]bool{}
	var out []Entry
	add := func(rel string, ver storage.Version, tracked bool) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		out = append(out, Entry{Path: rel, Version: ver, Tracked: tracked})
	}

	if root != "" {
		// A walk error is not fatal: an unreadable subdirectory should cost the
		// user that subdirectory, not the whole listing.
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if !isSpaceFile(rel) {
				return nil
			}
			ver, tracked := versions[rel]
			add(rel, ver, tracked)
			return nil
		})
	}

	// Whatever the log knows that the tree no longer has: deleted files whose
	// history is still recoverable.
	for rel, ver := range versions {
		add(rel, ver, true)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// isSpaceFile rejects the bookkeeping that lives alongside the documents.
func isSpaceFile(rel string) bool {
	switch {
	case rel == "", rel == "config.yaml":
		return false
	case strings.HasPrefix(rel, "."):
		return false
	case strings.HasPrefix(rel, storage.SnapshotPrefix),
		strings.HasPrefix(rel, "_health/"),
		strings.HasPrefix(rel, "_heads/"):
		return false
	case storage.IsFileLogInternal(rel):
		return false
	}
	return true
}
