package tui

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/editor"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/storage"
)

// editState is the editor picker + in-flight edit, shared by the client and
// server TUIs.
//
// Edits never touch the file on disk directly: the body is checked out of the
// FileLog, edited in a scratch file, and written back through FileLog.Put with
// the version captured at checkout. Writing to disk instead would skip the
// version log, so `file history` and the Web UI would never see the change —
// and on a server it would also bypass the ACL and audit trail.
type editState struct {
	picking bool
	choices []editor.Editor
	cursor  int

	pending *editor.Pending
	path    string
	opened  storage.Version
}

// editSavedMsg reports the outcome of an edit session.
type editSavedMsg struct {
	path    string
	version storage.Version
	changed bool
	err     error
}

// editorsFor builds the picker list, putting a remembered choice first so the
// common case is one keypress.
func editorsFor(remembered string) []editor.Editor {
	found := editor.Detect()
	if remembered == "" || len(found) == 0 {
		return found
	}
	for i, e := range found {
		if e.ID == remembered {
			out := append([]editor.Editor{e}, found[:i]...)
			return append(out, found[i+1:]...)
		}
	}
	// Remembered editor is no longer installed; fall back to what is.
	if e, err := editor.Lookup(remembered); err == nil {
		return append([]editor.Editor{e}, found...)
	}
	return found
}

// begin opens the picker for path. It reports an error message when no editor
// is installed, rather than silently doing nothing on Enter.
func (s *editState) begin(path, remembered string) string {
	// An editor is a shell escape. vim's :!sh, nano's ^R^X and emacs' M-! all
	// spawn a shell as whoever owns the session, so launching one where a shell
	// escape is forbidden would reopen exactly the hole shell.go closes: under
	// Model B the session is the locked-down contextd service user holding only
	// an app-key identity, and a shell there is privilege escalation past RBAC.
	if !hostShellAllowed() {
		return "editing disabled here (Model B / non-login SSH) — an editor is a shell escape; use the Web UI or contextd file edit on a real login"
	}
	choices := editorsFor(remembered)
	if len(choices) == 0 {
		return "no editor found — set $EDITOR, or install vim, nvim, nano, helix, micro or code"
	}
	s.picking = true
	s.choices = choices
	s.cursor = 0
	s.path = path
	return ""
}

func (s *editState) cancel() {
	s.picking = false
	s.choices = nil
	s.cursor = 0
	if s.pending != nil {
		s.pending.Cleanup()
		s.pending = nil
	}
}

func (s *editState) moveCursor(delta int) {
	if len(s.choices) == 0 {
		return
	}
	s.cursor += delta
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= len(s.choices) {
		s.cursor = len(s.choices) - 1
	}
}

func (s *editState) selected() (editor.Editor, bool) {
	if s.cursor < 0 || s.cursor >= len(s.choices) {
		return editor.Editor{}, false
	}
	return s.choices[s.cursor], true
}

// launch checks the file out and hands the editor command to Bubble Tea, which
// releases the terminal for the duration and reclaims it afterwards.
func (s *editState) launch(open func() (*storage.FileLog, error), save func(data []byte, expected storage.Version) (storage.Version, error)) tea.Cmd {
	ed, ok := s.selected()
	if !ok {
		return nil
	}
	s.picking = false
	path := s.path

	fl, err := open()
	if err != nil {
		return func() tea.Msg { return editSavedMsg{path: path, err: err} }
	}
	data, ver, err := fl.Get(context.Background(), path)
	if errors.Is(err, storage.ErrNotFound) {
		data, ver, err = nil, "", nil
	}
	if err != nil {
		return func() tea.Msg { return editSavedMsg{path: path, err: err} }
	}
	pending, err := editor.Prepare(ed, path, data)
	if err != nil {
		return func() tea.Msg { return editSavedMsg{path: path, err: err} }
	}
	s.pending = pending
	s.opened = ver
	logx.L().Info("tui edit opening", "path", path, "editor", ed.ID, "version", string(ver))

	opened := ver
	return tea.ExecProcess(pending.Command(), func(runErr error) tea.Msg {
		defer func() { pending.Cleanup() }()
		if runErr != nil {
			return editSavedMsg{path: path, err: fmt.Errorf("editor %s: %w", ed.Name, runErr)}
		}
		body, changed, err := pending.Finish()
		if err != nil {
			return editSavedMsg{path: path, err: err}
		}
		if !changed {
			logx.L().Info("tui edit unchanged", "path", path)
			return editSavedMsg{path: path, changed: false}
		}
		next, err := save(body, opened)
		if errors.Is(err, storage.ErrConflict) {
			return editSavedMsg{path: path, err: fmt.Errorf(
				"%s changed while you were editing (was %s) — reopen and reapply", path, storage.DisplayVersion(opened))}
		}
		if err != nil {
			return editSavedMsg{path: path, err: err}
		}
		logx.L().Info("tui edit saved", "path", path, "bytes", len(body), "version", string(next))
		return editSavedMsg{path: path, version: next, changed: true}
	})
}

// remember persists the chosen editor into the client config so the picker
// opens on that entry next time. Best-effort: a config that cannot be written
// must not lose the user's edit.
func (s *editState) remember(spaceRoot string) {
	ed, ok := s.selected()
	if !ok {
		return
	}
	cfg, err := config.Load(spaceRoot)
	if err != nil {
		return
	}
	if cfg.Editor == ed.ID {
		return
	}
	cfg.Editor = ed.ID
	if err := config.Save(cfg); err != nil {
		logx.L().Warn("remember editor choice", "editor", ed.ID, "err", err)
	}
}

// renderPicker draws the editor list as the detail pane content.
func (s *editState) renderPicker() string {
	if !s.picking {
		return ""
	}
	out := fmt.Sprintf("Edit %s\n\nOpen with:\n\n", s.path)
	for i, e := range s.choices {
		marker := "  "
		if i == s.cursor {
			marker = "❯ "
		}
		out += marker + e.String() + "\n"
	}
	out += "\nenter open · j/k move · esc cancel"
	return out
}
