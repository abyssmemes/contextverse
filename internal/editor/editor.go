// Package editor resolves which text editor to hand a file to, and builds the
// command that runs it.
//
// Two properties matter to callers and are easy to get wrong:
//
//   - A GUI editor forks and returns immediately unless told to wait. Launching
//     `code file.md` and then reading the file back yields the *unedited* body,
//     silently discarding the user's work. Every GUI entry below therefore
//     carries its wait flag, and a wait flag is added to $EDITOR/$VISUAL when
//     the user left it off.
//   - A terminal editor needs the calling program to release the terminal. That
//     is the caller's job (tea.ExecProcess in the TUI, direct stdio in the CLI);
//     Terminal reports which kind this is.
package editor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Editor is a resolved, runnable editor.
type Editor struct {
	ID       string   // stable identifier, e.g. "nvim"
	Name     string   // display name, e.g. "Neovim"
	Bin      string   // absolute path in PATH
	Args     []string // arguments inserted before the file path
	Terminal bool     // true = runs in this terminal; false = separate GUI window
	FromEnv  bool     // resolved from $VISUAL/$EDITOR
}

// String renders a picker label.
func (e Editor) String() string {
	label := e.Name
	if e.FromEnv {
		label += " (from $EDITOR)"
	}
	if !e.Terminal {
		label += " — opens a window"
	}
	return label
}

// Command builds the exec.Cmd that edits path. Stdio is left to the caller.
func (e Editor) Command(path string) *exec.Cmd {
	args := append(append([]string{}, e.Args...), path)
	return exec.Command(e.Bin, args...)
}

// candidate is a known editor, in preference order.
type candidate struct {
	id       string
	name     string
	args     []string
	terminal bool
}

// known lists editors we can launch correctly. Order is the picker order:
// terminal editors first, because the caller is already in a terminal.
var known = []candidate{
	{id: "nvim", name: "Neovim", terminal: true},
	{id: "vim", name: "Vim", terminal: true},
	{id: "vi", name: "vi", terminal: true},
	{id: "hx", name: "Helix", terminal: true},
	{id: "helix", name: "Helix", terminal: true},
	{id: "micro", name: "micro", terminal: true},
	{id: "nano", name: "nano", terminal: true},
	{id: "emacs", name: "Emacs", args: []string{"-nw"}, terminal: true},
	{id: "code", name: "VS Code", args: []string{"--wait"}},
	{id: "codium", name: "VSCodium", args: []string{"--wait"}},
	{id: "cursor", name: "Cursor", args: []string{"--wait"}},
	{id: "zed", name: "Zed", args: []string{"--wait"}},
	{id: "subl", name: "Sublime Text", args: []string{"--wait"}},
}

// waitFlags maps a GUI editor binary to the flag that makes it block. Used both
// for the built-in entries and to repair a $EDITOR that omits it.
var waitFlags = map[string]string{
	"code":   "--wait",
	"codium": "--wait",
	"cursor": "--wait",
	"zed":    "--wait",
	"subl":   "--wait",
}

// Detect returns the editors installed on this machine, $VISUAL/$EDITOR first.
// The slice is empty when nothing usable is installed.
func Detect() []Editor {
	var out []Editor
	seen := map[string]bool{}

	if e, ok := FromEnvironment(); ok {
		out = append(out, e)
		seen[e.Bin] = true
	}
	for _, c := range known {
		bin, err := exec.LookPath(c.id)
		if err != nil || seen[bin] {
			continue
		}
		seen[bin] = true
		out = append(out, Editor{
			ID:       c.id,
			Name:     c.name,
			Bin:      bin,
			Args:     c.args,
			Terminal: c.terminal,
		})
	}
	return out
}

// FromEnvironment resolves $VISUAL, then $EDITOR. The variable may carry its own
// arguments ("code --wait", "emacs -nw"); those are preserved, and a missing
// wait flag on a known GUI editor is added rather than silently losing edits.
func FromEnvironment() (Editor, bool) {
	raw := strings.TrimSpace(os.Getenv("VISUAL"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if raw == "" {
		return Editor{}, false
	}
	fields := strings.Fields(raw)
	bin, err := exec.LookPath(fields[0])
	if err != nil {
		return Editor{}, false
	}
	args := fields[1:]

	e := Editor{
		ID:       fields[0],
		Name:     fields[0],
		Bin:      bin,
		Args:     args,
		Terminal: true, // unknown binaries are assumed terminal — the common case
		FromEnv:  true,
	}
	if c, ok := lookupKnown(fields[0]); ok {
		e.Name = c.name
		e.Terminal = c.terminal
	}
	if flag, ok := waitFlags[baseName(fields[0])]; ok {
		e.Terminal = false
		if !hasArg(args, flag) {
			e.Args = append(append([]string{}, args...), flag)
		}
	}
	return e, true
}

// Lookup resolves a single editor by id, for --editor and for a remembered
// choice in config.
func Lookup(id string) (Editor, error) {
	if id == "" {
		return Editor{}, fmt.Errorf("empty editor id")
	}
	bin, err := exec.LookPath(id)
	if err != nil {
		return Editor{}, fmt.Errorf("editor %q not found in PATH", id)
	}
	e := Editor{ID: id, Name: id, Bin: bin, Terminal: true}
	if c, ok := lookupKnown(id); ok {
		e.Name = c.name
		e.Args = c.args
		e.Terminal = c.terminal
	}
	if flag, ok := waitFlags[baseName(id)]; ok {
		e.Terminal = false
		if !hasArg(e.Args, flag) {
			e.Args = append(e.Args, flag)
		}
	}
	return e, nil
}

func lookupKnown(id string) (candidate, bool) {
	base := baseName(id)
	for _, c := range known {
		if c.id == base {
			return c, true
		}
	}
	return candidate{}, false
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
