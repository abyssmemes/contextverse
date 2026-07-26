// Package prompt provides the interactive controls the CLI asks questions with:
// a single-choice picker, a multi-choice picker, and a text field.
//
// It exists so every question in contextd looks and behaves the same. Before it,
// init asked bare `? Your role [DevOps]:` lines with no explanation of what a
// choice meant, while `plugin install` asked users to type "1,3" or "all" into a
// free-text field — three surfaces, three interaction models, no descriptions.
//
// Every control here is arrow-key driven, shows a one-line description per
// option, and refuses to run when there is no terminal: callers must fall back
// to flags rather than hang a CI job on a prompt nobody can answer.
package prompt

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ErrNotInteractive is returned when there is no terminal to prompt on.
var ErrNotInteractive = errors.New("not an interactive terminal")

// ErrCancelled is returned when the user aborts with esc or ctrl+c.
var ErrCancelled = errors.New("cancelled")

// Choice is one selectable option. Desc is what makes the picker teachable —
// it is displayed under the highlighted entry, so a user meets an explanation
// at the moment of choosing rather than in documentation afterwards.
type Choice struct {
	ID    string
	Label string
	Desc  string
	Note  string // right-aligned hint, e.g. "detected", "recommended"
}

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#0f7a6c", Dark: "#2dd4bf"}
	colInk    = lipgloss.AdaptiveColor{Light: "#0b1f33", Dark: "#e8eef4"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#5a7085", Dark: "#8aa0b4"}

	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleLede  = lipgloss.NewStyle().Foreground(colMuted)
	styleSel   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleItem  = lipgloss.NewStyle().Foreground(colInk)
	styleDesc  = lipgloss.NewStyle().Foreground(colMuted).PaddingLeft(4)
	styleNote  = lipgloss.NewStyle().Foreground(colMuted).Italic(true)
	styleKeys  = lipgloss.NewStyle().Foreground(colMuted)
)

// Interactive reports whether stdin and stdout are both a terminal.
func Interactive() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		fi, err := f.Stat()
		if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
			return false
		}
	}
	return true
}

// Select asks for exactly one choice and returns its index.
func Select(title, lede string, choices []Choice, initial int) (int, error) {
	if !Interactive() {
		return 0, ErrNotInteractive
	}
	if len(choices) == 0 {
		return 0, fmt.Errorf("select %q: no choices", title)
	}
	if initial < 0 || initial >= len(choices) {
		initial = 0
	}
	m := selectModel{title: title, lede: lede, choices: choices, cursor: initial}
	out, err := tea.NewProgram(m).Run()
	if err != nil {
		return 0, err
	}
	res := out.(selectModel)
	if res.cancelled {
		return 0, ErrCancelled
	}
	return res.cursor, nil
}

// MultiSelect asks for zero or more choices and returns the chosen indexes.
// preselected marks entries checked on open — used to pre-tick detected AI
// clients so the common answer is a single Enter.
func MultiSelect(title, lede string, choices []Choice, preselected []bool) ([]int, error) {
	if !Interactive() {
		return nil, ErrNotInteractive
	}
	if len(choices) == 0 {
		return nil, nil
	}
	checked := make([]bool, len(choices))
	copy(checked, preselected)

	m := multiModel{title: title, lede: lede, choices: choices, checked: checked}
	out, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, err
	}
	res := out.(multiModel)
	if res.cancelled {
		return nil, ErrCancelled
	}
	var idx []int
	for i, on := range res.checked {
		if on {
			idx = append(idx, i)
		}
	}
	return idx, nil
}

// Text asks for a line of input. def is returned when the user submits empty.
func Text(label, lede, def string) (string, error) {
	if !Interactive() {
		return "", ErrNotInteractive
	}
	ti := textinput.New()
	ti.Placeholder = def
	ti.Focus()
	ti.Prompt = "› "
	ti.CharLimit = 512

	out, err := tea.NewProgram(textModel{label: label, lede: lede, def: def, input: ti}).Run()
	if err != nil {
		return "", err
	}
	res := out.(textModel)
	if res.cancelled {
		return "", ErrCancelled
	}
	v := strings.TrimSpace(res.input.Value())
	if v == "" {
		return def, nil
	}
	return v, nil
}

// Confirm asks a yes/no question. def is the answer Enter selects.
func Confirm(title, lede string, def bool) (bool, error) {
	initial := 1
	if def {
		initial = 0
	}
	i, err := Select(title, lede, []Choice{
		{ID: "yes", Label: "Yes"},
		{ID: "no", Label: "No"},
	}, initial)
	if err != nil {
		return false, err
	}
	return i == 0, nil
}

func header(title, lede string) string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(title) + "\n")
	if lede != "" {
		b.WriteString(styleLede.Render(lede) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

func renderChoice(c Choice, selected, checked, showCheck bool) string {
	cursor := "  "
	label := styleItem.Render(c.Label)
	if selected {
		cursor = styleSel.Render("❯ ")
		label = styleSel.Render(c.Label)
	}
	box := ""
	if showCheck {
		box = "[ ] "
		if checked {
			box = "[x] "
		}
	}
	line := cursor + box + label
	if c.Note != "" {
		line += "  " + styleNote.Render("("+c.Note+")")
	}
	return line
}

// --- single select ---

type selectModel struct {
	title     string
	lede      string
	choices   []Choice
	cursor    int
	cancelled bool
	done      bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "esc", "q":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			m.done = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = len(m.choices) - 1
		}
	}
	return m, nil
}

func (m selectModel) View() string {
	if m.done || m.cancelled {
		return ""
	}
	var b strings.Builder
	b.WriteString(header(m.title, m.lede))
	for i, c := range m.choices {
		b.WriteString(renderChoice(c, i == m.cursor, false, false) + "\n")
		if i == m.cursor && c.Desc != "" {
			b.WriteString(styleDesc.Render(c.Desc) + "\n")
		}
	}
	b.WriteString("\n" + styleKeys.Render("↑/↓ move · enter select · esc cancel"))
	return b.String() + "\n"
}

// --- multi select ---

type multiModel struct {
	title     string
	lede      string
	choices   []Choice
	checked   []bool
	cursor    int
	cancelled bool
	done      bool
}

func (m multiModel) Init() tea.Cmd { return nil }

func (m multiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			m.done = true
			return m, tea.Quit
		case " ":
			m.checked[m.cursor] = !m.checked[m.cursor]
		case "a":
			all := true
			for _, on := range m.checked {
				if !on {
					all = false
					break
				}
			}
			for i := range m.checked {
				m.checked[i] = !all
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m multiModel) View() string {
	if m.done || m.cancelled {
		return ""
	}
	var b strings.Builder
	b.WriteString(header(m.title, m.lede))
	for i, c := range m.choices {
		b.WriteString(renderChoice(c, i == m.cursor, m.checked[i], true) + "\n")
		if i == m.cursor && c.Desc != "" {
			b.WriteString(styleDesc.Render(c.Desc) + "\n")
		}
	}
	b.WriteString("\n" + styleKeys.Render("↑/↓ move · space toggle · a all/none · enter confirm · esc cancel"))
	return b.String() + "\n"
}

// --- text ---

type textModel struct {
	label     string
	lede      string
	def       string
	input     textinput.Model
	cancelled bool
	done      bool
}

func (m textModel) Init() tea.Cmd { return textinput.Blink }

func (m textModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			m.done = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m textModel) View() string {
	if m.done || m.cancelled {
		return ""
	}
	var b strings.Builder
	b.WriteString(header(m.label, m.lede))
	b.WriteString(m.input.View() + "\n")
	if m.def != "" {
		b.WriteString("\n" + styleKeys.Render("enter keeps “"+m.def+"” · esc cancel"))
	} else {
		b.WriteString("\n" + styleKeys.Render("enter confirm · esc cancel"))
	}
	return b.String() + "\n"
}
