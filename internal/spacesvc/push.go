package spacesvc

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/orkcom-tech/contextverse/internal/logx"
	"github.com/orkcom-tech/contextverse/internal/storage"
)

// Push applies a batch of operations against the space's expected head.
//
// # What "transactionally" has to mean
//
// The doc comment said transactional and the code applied operations one at a
// time with no lock and no undo. Fail on the third of five and the first two
// were already in storage and in the working tree, while head never moved — so
// clients pulling by head never learned about them, and the space quietly held
// content nobody had a version marker for. Two clients pushing at once both read
// the same head, both wrote their files, and one then got a conflict on a head
// it had already overwritten the contents behind.
//
// It now works in three phases:
//
//  1. Plan. Decode, reject unknown operations, run the secret hooks, check every
//     file against its size limit, and check the whole batch against the space
//     quota once — as a batch, not per operation, which is also what stops a
//     push of N files walking the entire space N times.
//  2. Apply, holding the space's lock so no other write interleaves. Every
//     applied operation records how to undo itself.
//  3. Commit by advancing head. Nothing is visible to a puller until this.
//
// A failure during apply rolls back what was applied. If the rollback itself
// fails there is nothing honest left to do but say so: the error names the
// space as mixed rather than reporting a clean failure over a dirty one.
func (s *Service) Push(ctx context.Context, name string, req PushRequest) (*PushResult, error) {
	unlock := s.lockSpace(name)
	defer unlock()

	b, err := s.OpenBackend(name)
	if err != nil {
		return nil, err
	}
	cur, err := b.Head(ctx, storage.SpaceScope)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	if errors.Is(err, storage.ErrNotFound) {
		cur = ""
	}
	if string(cur) != req.ExpectedHead {
		return nil, &storage.ConflictError{
			Path:     "head:" + storage.SpaceScope,
			Expected: storage.Version(req.ExpectedHead),
			Actual:   cur,
		}
	}

	fl := &storage.FileLog{Backend: b}
	planned, err := s.planPush(ctx, name, fl, req.Ops)
	if err != nil {
		return nil, err
	}

	undo, err := s.applyPush(ctx, name, fl, planned)
	if err != nil {
		if rerr := s.rollbackPush(ctx, name, fl, undo); rerr != nil {
			logx.L().Error("push rollback failed; space holds a partial batch",
				"space", name, "apply_err", err, "rollback_err", rerr)
			return nil, fmt.Errorf("push failed and could not be undone — space %q holds part of the batch: %w (rollback: %v)", name, err, rerr)
		}
		return nil, err
	}

	next := newHeadID()
	if err := b.SetHead(ctx, storage.SpaceScope, cur, storage.Version(next)); err != nil {
		// The head is the commit point, so failing here means none of this
		// happened as far as any reader is concerned. Undo it.
		if rerr := s.rollbackPush(ctx, name, fl, undo); rerr != nil {
			logx.L().Error("head advance failed and rollback failed",
				"space", name, "head_err", err, "rollback_err", rerr)
			return nil, fmt.Errorf("push applied but head could not advance and the batch could not be undone — space %q is inconsistent: %w (rollback: %v)", name, err, rerr)
		}
		return nil, err
	}
	return &PushResult{Head: next, Applied: len(planned)}, nil
}

// plannedOp is one validated operation, ready to apply.
type plannedOp struct {
	op       string
	path     string
	data     []byte
	expected storage.Version
}

// planPush validates the whole batch before any of it is written.
//
// Everything that can be known in advance is decided here: a push that will be
// refused should be refused before it has changed anything, not halfway through.
func (s *Service) planPush(ctx context.Context, name string, fl *storage.FileLog, ops []PushOp) ([]plannedOp, error) {
	q := s.QuotasFor(name)

	// One inventory for the batch. Checking the space quota per operation meant
	// a push of N files walked the whole space N times, and each walk read every
	// object; the cost was quadratic in a batch a sync client sends routinely.
	sizes, total, err := s.inventory(ctx, name)
	if err != nil {
		return nil, err
	}
	count := len(sizes)

	planned := make([]plannedOp, 0, len(ops))
	for _, op := range ops {
		switch op.Op {
		case "put":
			data, err := decodeB64(op.ContentB64)
			if err != nil {
				return nil, fmt.Errorf("op put %s: %w", op.Path, err)
			}
			if err := s.Hooks.CheckPut(op.Path, data); err != nil {
				return nil, err
			}
			if err := q.CheckFileSize(int64(len(data))); err != nil {
				return nil, err
			}
			expected := storage.Version(op.Expected)
			if op.Expected == "" {
				// Infer: create when absent, otherwise require the live version.
				if _, ver, gerr := fl.Get(ctx, op.Path); gerr == nil {
					expected = ver
				} else if !errors.Is(gerr, storage.ErrNotFound) {
					return nil, gerr
				}
			} else if err := checkCAS(ctx, fl, op.Path, expected); err != nil {
				// Stale version markers are the ordinary reason a push is
				// refused, so they are found while nothing has been written.
				// The space lock means nothing can move between here and the
				// apply, which leaves I/O as the only way apply still fails.
				return nil, err
			}
			// Account the batch as it will land, so a push that only fits
			// because of its own deletes is judged on the net effect.
			old, existed := sizes[op.Path]
			if !existed {
				count++
			}
			total += int64(len(data)) - old
			sizes[op.Path] = int64(len(data))
			planned = append(planned, plannedOp{op: "put", path: op.Path, data: data, expected: expected})

		case "delete":
			expected := storage.Version(op.Expected)
			if op.Expected == "" {
				_, ver, gerr := fl.Get(ctx, op.Path)
				if gerr != nil {
					return nil, gerr
				}
				expected = ver
			} else if err := checkCAS(ctx, fl, op.Path, expected); err != nil {
				return nil, err
			}
			if old, existed := sizes[op.Path]; existed {
				total -= old
				count--
				delete(sizes, op.Path)
			}
			planned = append(planned, plannedOp{op: "delete", path: op.Path, expected: expected})

		default:
			return nil, fmt.Errorf("unknown op %q", op.Op)
		}
	}

	// Checked against the batch's end state rather than op by op.
	if err := q.CheckSpace(total, count, 0, 0); err != nil {
		return nil, err
	}
	return planned, nil
}

// undoOp is how to put one applied operation back.
type undoOp struct {
	path string
	// created is set when the put brought the path into existence, so undoing
	// it means removing it rather than restoring anything.
	created bool
	// prevVersion is the version that was live before a put replaced it, or the
	// version a delete removed. Bodies are not captured up front: the file log
	// keeps them, and reading every old file to prepare for a failure that
	// almost never happens would cost the happy path twice over.
	prevVersion int
	deleted     bool
}

func (s *Service) applyPush(ctx context.Context, name string, fl *storage.FileLog, planned []plannedOp) ([]undoOp, error) {
	undo := make([]undoOp, 0, len(planned))
	for _, op := range planned {
		switch op.op {
		case "put":
			prev, existed := livePutVersion(ctx, fl, op.path)
			if _, err := fl.Put(ctx, op.path, op.data, op.expected); err != nil {
				return undo, err
			}
			if err := s.writeTreeFile(name, op.path, op.data); err != nil {
				return undo, err
			}
			undo = append(undo, undoOp{path: op.path, created: !existed, prevVersion: prev})

		case "delete":
			prev, _ := livePutVersion(ctx, fl, op.path)
			if err := fl.SoftDelete(ctx, op.path, op.expected); err != nil {
				return undo, err
			}
			s.removeTreeFile(name, op.path)
			undo = append(undo, undoOp{path: op.path, deleted: true, prevVersion: prev})
		}
	}
	return undo, nil
}

// rollbackPush reverses applied operations, newest first.
func (s *Service) rollbackPush(ctx context.Context, name string, fl *storage.FileLog, undo []undoOp) error {
	var firstErr error
	fail := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for i := len(undo) - 1; i >= 0; i-- {
		u := undo[i]
		switch {
		case u.deleted:
			if _, err := fl.Undelete(ctx, u.path); err != nil {
				fail(fmt.Errorf("restore %s: %w", u.path, err))
				continue
			}
			if data, _, err := fl.Get(ctx, u.path); err == nil {
				fail(s.writeTreeFile(name, u.path, data))
			}

		case u.created:
			live, err := fl.LiveVersion(ctx, u.path)
			if err != nil {
				fail(fmt.Errorf("remove %s: %w", u.path, err))
				continue
			}
			if err := fl.SoftDelete(ctx, u.path, live); err != nil {
				fail(fmt.Errorf("remove %s: %w", u.path, err))
				continue
			}
			s.removeTreeFile(name, u.path)

		default:
			body, _, err := fl.GetVersion(ctx, u.path, u.prevVersion)
			if err != nil {
				fail(fmt.Errorf("read previous %s v%d: %w", u.path, u.prevVersion, err))
				continue
			}
			live, err := fl.LiveVersion(ctx, u.path)
			if err != nil {
				fail(fmt.Errorf("restore %s: %w", u.path, err))
				continue
			}
			if _, err := fl.Put(ctx, u.path, body, live); err != nil {
				fail(fmt.Errorf("restore %s: %w", u.path, err))
				continue
			}
			fail(s.writeTreeFile(name, u.path, body))
		}
	}
	return firstErr
}

// checkCAS rejects a version marker that no longer matches the live file, so a
// stale push is refused during planning rather than half-applied.
func checkCAS(ctx context.Context, fl *storage.FileLog, path string, expected storage.Version) error {
	live, err := fl.LiveVersion(ctx, path)
	if errors.Is(err, storage.ErrNotFound) {
		live = ""
	} else if err != nil {
		return err
	}
	want, werr := storage.ParseFileVersion(expected)
	got, gerr := storage.ParseFileVersion(live)
	if werr == nil && gerr == nil && want == got {
		return nil
	}
	// Content-hash markers from older clients are compared as written.
	if werr != nil && expected == live {
		return nil
	}
	return &storage.ConflictError{Path: path, Expected: expected, Actual: live}
}

// livePutVersion reports the live integer version of a path and whether it
// exists at all.
func livePutVersion(ctx context.Context, fl *storage.FileLog, path string) (int, bool) {
	ver, err := fl.LiveVersion(ctx, path)
	if err != nil {
		return 0, false
	}
	n, err := storage.ParseFileVersion(ver)
	if err != nil {
		return 0, false
	}
	return n, n > 0
}

// inventory returns the on-disk size of every file in the space and their total.
//
// A list failure is an error rather than a pass. The previous behaviour returned
// nil — "don't block on list failure" — which made an unreadable space an
// unlimited one, the exact failure QuotasFor's own comment warns about two
// screens above.
func (s *Service) inventory(ctx context.Context, name string) (map[string]int64, int64, error) {
	entries, err := s.Tree(ctx, name)
	if err != nil {
		return nil, 0, fmt.Errorf("read space contents for the quota check: %w", err)
	}
	sizes := make(map[string]int64, len(entries))
	var total int64
	for _, e := range entries {
		p, err := s.treePath(name, e.Path)
		if err != nil {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		sizes[e.Path] = st.Size()
		total += st.Size()
	}
	return sizes, total, nil
}
