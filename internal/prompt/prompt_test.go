package prompt

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

var demo = []Choice{
	{ID: "solo", Label: "Solo", Desc: "Local only, no server."},
	{ID: "client", Label: "Client", Desc: "Join a team's server."},
	{ID: "server", Label: "Server", Desc: "Host for a team."},
}

func TestSelectMovesAndConfirms(t *testing.T) {
	m := selectModel{choices: demo}

	next, _ := m.Update(key("j"))
	m = next.(selectModel)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d after j, want 1", m.cursor)
	}
	next, _ = m.Update(key("enter"))
	m = next.(selectModel)
	if !m.done || m.cancelled {
		t.Errorf("done = %v cancelled = %v, want done", m.done, m.cancelled)
	}
}

func TestSelectStopsAtBothEnds(t *testing.T) {
	m := selectModel{choices: demo}

	next, _ := m.Update(key("k"))
	if next.(selectModel).cursor != 0 {
		t.Error("cursor moved above the first entry")
	}
	m = selectModel{choices: demo, cursor: len(demo) - 1}
	next, _ = m.Update(key("j"))
	if next.(selectModel).cursor != len(demo)-1 {
		t.Error("cursor moved past the last entry")
	}
}

func TestSelectEscCancels(t *testing.T) {
	m := selectModel{choices: demo}
	next, _ := m.Update(key("esc"))
	if !next.(selectModel).cancelled {
		t.Error("esc did not cancel")
	}
}

// The description under the highlighted entry is the whole point of the picker:
// a user should learn what a choice means while choosing it.
func TestSelectShowsDescriptionOfHighlightedChoiceOnly(t *testing.T) {
	m := selectModel{choices: demo}
	view := m.View()

	if !strings.Contains(view, "Local only, no server.") {
		t.Errorf("description of the highlighted choice missing:\n%s", view)
	}
	if strings.Contains(view, "Host for a team.") {
		t.Errorf("description of an unselected choice leaked:\n%s", view)
	}
}

func TestMultiSelectTogglesAndSelectsAll(t *testing.T) {
	m := multiModel{choices: demo, checked: make([]bool, len(demo))}

	next, _ := m.Update(key("space"))
	m = next.(multiModel)
	if !m.checked[0] {
		t.Fatal("space did not check the entry under the cursor")
	}
	next, _ = m.Update(key("space"))
	m = next.(multiModel)
	if m.checked[0] {
		t.Fatal("space did not uncheck on second press")
	}

	next, _ = m.Update(key("a"))
	m = next.(multiModel)
	for i, on := range m.checked {
		if !on {
			t.Fatalf("a left entry %d unchecked", i)
		}
	}
	next, _ = m.Update(key("a"))
	m = next.(multiModel)
	for i, on := range m.checked {
		if on {
			t.Fatalf("second a left entry %d checked", i)
		}
	}
}

// Detected clients arrive pre-ticked so the common answer is one keypress.
func TestMultiSelectHonoursPreselection(t *testing.T) {
	m := multiModel{choices: demo, checked: []bool{false, true, false}}
	view := m.View()
	if !strings.Contains(view, "[x]") {
		t.Errorf("preselected entry not rendered as checked:\n%s", view)
	}
	if strings.Count(view, "[x]") != 1 {
		t.Errorf("want exactly one checked entry:\n%s", view)
	}
}

func TestMultiSelectEscCancels(t *testing.T) {
	m := multiModel{choices: demo, checked: make([]bool, len(demo))}
	next, _ := m.Update(key("esc"))
	if !next.(multiModel).cancelled {
		t.Error("esc did not cancel")
	}
}

func TestTextKeepsDefaultOnEmptySubmit(t *testing.T) {
	m := textModel{def: "English"}
	next, _ := m.Update(key("enter"))
	res := next.(textModel)
	if !res.done {
		t.Fatal("enter did not submit")
	}
	if got := strings.TrimSpace(res.input.Value()); got != "" {
		t.Fatalf("value = %q, want empty so the default applies", got)
	}
}

// A prompt in a pipeline would hang forever waiting for input nobody can give.
func TestControlsRefuseWithoutATerminal(t *testing.T) {
	if Interactive() {
		t.Skip("test run attached to a terminal")
	}
	if _, err := Select("t", "", demo, 0); err != ErrNotInteractive {
		t.Errorf("Select err = %v, want ErrNotInteractive", err)
	}
	if _, err := MultiSelect("t", "", demo, nil); err != ErrNotInteractive {
		t.Errorf("MultiSelect err = %v, want ErrNotInteractive", err)
	}
	if _, err := Text("t", "", "d"); err != ErrNotInteractive {
		t.Errorf("Text err = %v, want ErrNotInteractive", err)
	}
}
