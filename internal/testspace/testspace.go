// Package testspace builds context spaces in the states real installations are
// actually in, for tests that need more than a freshly created one.
//
// # Why this exists
//
// Every test in this repository used to start from a space this build had just
// created. That is the one state a user is almost never in: they have a space
// from an earlier version, carried across an upgrade. Four separate bugs
// shipped because of it — `file list` reporting "(no files)" on a space full of
// documents, the TUI Files tab blank with nothing to open, the Space tab
// counting files the Files tab denied, and `space-index.md` frozen in a format
// no code writes any more.
//
// None of those were reachable from a fresh space, so no amount of testing the
// happy path would have found them. They were found by a person on a second
// laptop.
package testspace

import (
	"os"
	"path/filepath"
	"testing"
)

// Legacy returns a space in the shape a pre-v0.7 build left behind:
//
//   - documents written straight to the working tree, so the version log knows
//     nothing about any of them;
//   - no .contextverse directory at all;
//   - space-index.md in the old hardcoded format, with em dashes where the
//     dependency columns should be;
//   - config.yaml carrying a relative space_root, which is what earlier builds
//     recorded when --dir was relative.
//
// Anything reading this space has to cope, because this is what upgrading onto
// a new binary looks like.
func Legacy(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"config.yaml": "mode: solo\nspace_root: " + root + "\nidentity:\n    name: test\n    role: test\n    language: English\ntemplate: solo-default\nbackend:\n    driver: local\n",

		"context-entry.md": `# ContextVerse Context Entry

Read files in this order:

1. ` + "`identity/me.md`" + ` — who you're talking to
2. ` + "`team/principles.md`" + ` — how we work
3. ` + "`space-index.md`" + ` — what exists in this space
4. ` + "`decisions.md`" + ` — key decisions and why
`,

		// The old generated index: a format string with nothing in the columns.
		"space-index.md": `# Space Index
Last validated: 2026-07-22

## Projects
| Project | Status | Owner | Dependencies | Last validated |
|---------|--------|-------|--------------|----------------|
| — | — | — | — | — |

## Key Files
- context-entry.md — routing for any AI
- identity/me.md — who you are
- team/principles.md — how we work
`,

		"decisions.md": "# Decisions\n\nDecision log. Newest first.\n",

		"identity/me.md": `---
freshness: current
last-validated: 2026-07-22
stale-after: 90d
importance: high
---

# Me

- **Name:** test
- **Role:** test
`,

		"team/principles.md": "---\nimportance: high\n---\n\n# Principles\n\nHow we work.\n",
		"team/skill-map.md":  "# Skill Map\n",
		"team/space-map.md":  "# Space Map\n\nA drawing nobody regenerates.\n",
		"projects/README.md": "# Projects\n\nOne folder per project.\n",
	}

	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// LegacyDocuments is what Legacy wrote, for tests that assert every surface
// accounts for the same set.
func LegacyDocuments() []string {
	return []string{
		"context-entry.md",
		"decisions.md",
		"identity/me.md",
		"projects/README.md",
		"space-index.md",
		"team/principles.md",
		"team/skill-map.md",
		"team/space-map.md",
	}
}
