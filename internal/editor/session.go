package editor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Pending is an edit in progress: a scratch copy of the body on disk, waiting
// for an editor to run over it.
//
// It is split into Prepare / Command / Finish because the two callers drive the
// process differently. The CLI runs the editor itself and can use Session; the
// TUI must hand the *exec.Cmd to Bubble Tea so the framework can release and
// reclaim the terminal, and only then read the result back.
type Pending struct {
	tmp  string
	ed   Editor
	orig []byte
}

// Prepare writes data to a scratch file the editor can open. The original
// path's extension is preserved so the editor applies the right syntax mode.
func Prepare(ed Editor, nameHint string, data []byte) (*Pending, error) {
	ext := filepath.Ext(nameHint)
	base := strings.TrimSuffix(filepath.Base(nameHint), ext)
	if base == "" || base == "." {
		base = "context"
	}
	f, err := os.CreateTemp("", fmt.Sprintf("contextd-%s-*%s", sanitize(base), ext))
	if err != nil {
		return nil, fmt.Errorf("create scratch file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, fmt.Errorf("write scratch file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return nil, fmt.Errorf("close scratch file: %w", err)
	}
	return &Pending{tmp: f.Name(), ed: ed, orig: append([]byte{}, data...)}, nil
}

// Command builds the editor invocation. The caller wires stdio.
func (p *Pending) Command() *exec.Cmd { return p.ed.Command(p.tmp) }

// Path is the scratch file being edited.
func (p *Pending) Path() string { return p.tmp }

// Finish reads the edited body back and removes the scratch file. changed is
// false when the user saved nothing new, which callers use to skip writing a
// version that would be identical to the current one.
func (p *Pending) Finish() (out []byte, changed bool, err error) {
	defer p.Cleanup()
	out, err = os.ReadFile(p.tmp)
	if err != nil {
		return nil, false, fmt.Errorf("read back edited file: %w", err)
	}
	return out, !bytes.Equal(out, p.orig), nil
}

// Cleanup removes the scratch file. Safe to call twice.
func (p *Pending) Cleanup() {
	if p.tmp != "" {
		os.Remove(p.tmp)
	}
}

// Session runs a full edit synchronously against the current terminal. Used by
// the CLI, where nothing else owns stdio.
func Session(ed Editor, nameHint string, data []byte) (out []byte, changed bool, err error) {
	p, err := Prepare(ed, nameHint, data)
	if err != nil {
		return nil, false, err
	}
	cmd := p.Command()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		p.Cleanup()
		return nil, false, fmt.Errorf("editor %s exited with error: %w", ed.Name, err)
	}
	return p.Finish()
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
}
