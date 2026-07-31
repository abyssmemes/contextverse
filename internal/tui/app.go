package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/orkcom-tech/contextverse/internal/config"
	"github.com/orkcom-tech/contextverse/internal/graph"
	"github.com/orkcom-tech/contextverse/internal/storage"
	"github.com/orkcom-tech/contextverse/internal/version"
)

type clientTab int

const (
	tabSpace clientTab = iota
	tabProjects
	tabFiles
	tabPlugins
	tabOutput
	tabGraph
	tabHelp
)

// Graph is appended rather than slotted next to Files, where it arguably
// belongs: renumbering the existing tabs would break the muscle memory of
// anyone already using them, for a tidier list nobody asked for.
var clientTabNames = []string{"1 Space", "2 Projects", "3 Files", "4 Plugins", "5 Output", "6 Graph", "? Help"}

type model struct {
	spaceRoot  string
	cwd        string
	width      int
	height     int
	snap       Snapshot
	tab        clientTab
	cursor     int
	focusRight bool // Output viewport focused for scrolling
	busy       bool
	quitting   bool
	spin       spinner.Model
	vp         viewport.Model
	ready      bool

	// Files tab (version switch)
	files        []TrackedFile
	filePath     string
	fileVersions []FileVersionRow
	fileVerMode  bool // true = browsing versions of filePath
	filesErr     string

	edit editState

	// Graph tab
	graph      *graph.Graph
	graphErr   string
	graphFocus string // when set, showing this document's neighbourhood
}

type refreshMsg Snapshot
type actionDoneMsg struct {
	msg string
	err error
}

type graphLoadedMsg struct {
	g   *graph.Graph
	err error
}

type filesLoadedMsg struct {
	files []TrackedFile
	err   error
}

type fileVersionsMsg struct {
	path     string
	versions []FileVersionRow
	err      error
}

func newModel(spaceRoot, cwd string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)
	return model{
		spaceRoot: spaceRoot,
		cwd:       cwd,
		snap:      LoadSnapshot(spaceRoot),
		tab:       tabSpace,
		spin:      sp,
		vp:        viewport.New(20, 10),
	}
}

func (m model) Init() tea.Cmd {
	return m.spin.Tick
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.resizeViewport()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case refreshMsg:
		out := m.snap.Output
		last := m.snap.LastMsg
		err := m.snap.Err
		m.snap = Snapshot(msg)
		if out != "" && m.snap.Output == "" {
			m.snap.Output = out
		}
		if last != "" && m.snap.LastMsg == "" {
			m.snap.LastMsg = last
		}
		if err != "" && m.snap.Err == "" {
			m.snap.Err = err
		}
		m.busy = false
		m.syncViewportContent()
		return m, nil

	case actionDoneMsg:
		m.busy = false
		m.snap.Output = msg.msg
		if msg.err != nil {
			m.snap.Err = msg.err.Error()
			m.snap.LastMsg = firstLine(msg.msg)
			if m.snap.LastMsg == "" {
				m.snap.LastMsg = msg.err.Error()
			}
		} else {
			m.snap.Err = ""
			m.snap.LastMsg = firstLine(msg.msg)
			if m.snap.LastMsg == "" {
				m.snap.LastMsg = "ok"
			}
		}
		m.tab = tabOutput
		m.focusRight = true
		m.resizeViewport()
		cmds = append(cmds, refreshCmd(m.spaceRoot), m.spin.Tick)
		return m, tea.Batch(cmds...)

	case graphLoadedMsg:
		m.busy = false
		if msg.err != nil {
			m.graphErr = msg.err.Error()
			return m, nil
		}
		m.graph = msg.g
		m.graphErr = ""
		return m, nil

	case filesLoadedMsg:
		m.busy = false
		m.files = msg.files
		m.filesErr = ""
		if msg.err != nil {
			m.filesErr = msg.err.Error()
		}
		if m.cursor >= len(m.files) {
			m.cursor = 0
		}
		return m, nil

	case fileVersionsMsg:
		m.busy = false
		if msg.err != nil {
			m.snap.Err = msg.err.Error()
			m.filesErr = msg.err.Error()
			return m, nil
		}
		m.filePath = msg.path
		m.fileVersions = msg.versions
		m.fileVerMode = true
		m.cursor = 0
		m.filesErr = ""
		return m, nil

	case editSavedMsg:
		m.busy = false
		m.edit.pending = nil
		switch {
		case msg.err != nil:
			m.snap.Err = msg.err.Error()
			m.snap.LastMsg = msg.err.Error()
			return m, nil
		case !msg.changed:
			m.snap.Err = ""
			m.snap.LastMsg = "no changes to " + msg.path
			return m, nil
		default:
			m.snap.Err = ""
			m.snap.LastMsg = fmt.Sprintf("saved %s → %s", msg.path, storage.DisplayVersion(msg.version))
			// Reload the list so the new vN is visible immediately.
			return m, tea.Batch(loadFilesCmd(m.spaceRoot), refreshCmd(m.spaceRoot))
		}

	case shellDoneMsg:
		if msg.err != nil {
			m.snap.LastMsg = "shell: " + msg.err.Error()
		} else {
			m.snap.LastMsg = "returned from shell"
		}
		return m, nil

	case shellDeniedMsg:
		m.snap.LastMsg = shellDeniedFlash()
		return m, nil

	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.vp.LineUp(3)
		case tea.MouseButtonWheelDown:
			m.vp.LineDown(3)
		}
		return m, nil

	case tea.KeyMsg:
		if m.busy {
			switch msg.String() {
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}

		// Viewport scroll when on Output or focusRight
		if m.tab == tabOutput || (m.focusRight && m.tab == tabSpace) {
			switch msg.String() {
			case "pgdown", "ctrl+d":
				m.vp.HalfViewDown()
				return m, nil
			case "pgup", "ctrl+u":
				m.vp.HalfViewUp()
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			m.busy = true
			if m.tab == tabFiles {
				if m.fileVerMode && m.filePath != "" {
					return m, tea.Batch(loadFileVersionsCmd(m.spaceRoot, m.filePath), m.spin.Tick)
				}
				return m, tea.Batch(loadFilesCmd(m.spaceRoot), m.spin.Tick)
			}
			return m, tea.Batch(refreshCmd(m.spaceRoot), m.spin.Tick)
		case "?":
			m.tab = tabHelp
			m.cursor = 0
			return m, nil
		case "tab":
			m.focusRight = !m.focusRight
			return m, nil
		case "1":
			m.tab = tabSpace
			m.cursor = 0
			return m, nil
		case "2":
			m.tab = tabProjects
			m.cursor = 0
			return m, nil
		case "3", "f":
			m.tab = tabFiles
			m.cursor = 0
			m.fileVerMode = false
			m.filePath = ""
			return m, tea.Batch(loadFilesCmd(m.spaceRoot), m.spin.Tick)
		case "4", "p":
			m.tab = tabPlugins
			m.cursor = 0
			return m, nil
		case "5", "o":
			m.tab = tabOutput
			m.focusRight = true
			m.resizeViewport()
			return m, nil
		case "6":
			m.tab = tabGraph
			m.cursor = 0
			m.graphFocus = ""
			m.busy = true
			return m, tea.Batch(loadGraphCmd(m.spaceRoot), m.spin.Tick)
		case "esc":
			if m.edit.picking {
				m.edit.cancel()
				return m, nil
			}
			if m.tab == tabGraph && m.graphFocus != "" {
				m.graphFocus = ""
				m.cursor = 0
				return m, nil
			}
			if m.tab == tabFiles && m.fileVerMode {
				m.fileVerMode = false
				m.filePath = ""
				m.fileVersions = nil
				m.cursor = 0
				return m, nil
			}
			m.tab = tabSpace
			m.focusRight = false
			m.cursor = 0
			return m, nil
		case "a":
			m.busy = true
			m.snap.LastMsg = "activate…"
			return m, tea.Batch(runActionCmd(ActionActivate, m.spaceRoot, m.cwd), m.spin.Tick)
		case "i":
			m.busy = true
			m.snap.LastMsg = "plugin install…"
			return m, tea.Batch(runActionCmd(ActionPluginInstall, m.spaceRoot, m.cwd), m.spin.Tick)
		case "s":
			m.busy = true
			return m, tea.Batch(runActionCmd(ActionStatus, m.spaceRoot, m.cwd), m.spin.Tick)
		case "!":
			// Model A / local: spawn $SHELL; exit returns to TUI. q quits to parent shell.
			m.snap.LastMsg = "shell…"
			return m, spawnHostShellCmd()
		case "u":
			if m.snap.Mode == "client" {
				m.busy = true
				return m, tea.Batch(runActionCmd(ActionPull, m.spaceRoot, m.cwd), m.spin.Tick)
			}
			return m, nil
		case "U":
			if m.snap.Mode == "client" {
				m.busy = true
				return m, tea.Batch(runActionCmd(ActionPush, m.spaceRoot, m.cwd), m.spin.Tick)
			}
			return m, nil
		case "enter":
			if m.tab == tabGraph {
				if p := m.graphSelection(); p != "" {
					m.graphFocus = p
					m.cursor = 0
				}
				return m, nil
			}
			if m.tab != tabFiles {
				return m, nil
			}
			// Picker open: Enter picks the editor and launches it.
			if m.edit.picking {
				m.edit.remember(m.spaceRoot)
				return m, m.launchEdit()
			}
			// Version list: Enter previews, as before.
			if m.fileVerMode {
				m.busy = true
				return m, tea.Batch(m.filesPreview(), m.spin.Tick)
			}
			// File list: Enter edits — the action a file list implies.
			if m.cursor >= 0 && m.cursor < len(m.files) {
				if flash := m.edit.begin(m.files[m.cursor].Path, m.editorPreference()); flash != "" {
					m.snap.LastMsg = flash
				}
			}
			return m, nil
		case "V":
			// Version history moved off Enter when Enter became edit.
			if m.tab == tabFiles && !m.fileVerMode && !m.edit.picking {
				m.busy = true
				return m, tea.Batch(m.filesEnter(), m.spin.Tick)
			}
			return m, nil
		case "R":
			if m.tab == tabFiles && m.fileVerMode {
				m.busy = true
				return m, tea.Batch(m.filesRestore(), m.spin.Tick)
			}
			return m, nil
		case "v":
			if m.tab != tabFiles || m.edit.picking {
				return m, nil
			}
			m.busy = true
			if m.fileVerMode {
				return m, tea.Batch(m.filesPreview(), m.spin.Tick)
			}
			// Previously dead on the file list: preview the live version.
			return m, tea.Batch(m.filesPreviewCurrent(), m.spin.Tick)
		case "up", "k":
			if m.edit.picking {
				m.edit.moveCursor(-1)
				return m, nil
			}
			if m.tab == tabOutput {
				m.vp.LineUp(1)
				return m, nil
			}
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.edit.picking {
				m.edit.moveCursor(1)
				return m, nil
			}
			if m.tab == tabOutput {
				m.vp.LineDown(1)
				return m, nil
			}
			if m.cursor < m.maxCursor() {
				m.cursor++
			}
			return m, nil
		case "g":
			if m.tab == tabOutput {
				m.vp.GotoTop()
			} else {
				m.cursor = 0
			}
			return m, nil
		case "G":
			if m.tab == tabOutput {
				m.vp.GotoBottom()
			} else {
				m.cursor = m.maxCursor()
			}
			return m, nil
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *model) resizeViewport() {
	bodyH := m.height - 5
	if bodyH < 3 {
		bodyH = 3
	}
	m.vp.Width = max(10, m.width-4)
	m.vp.Height = max(3, bodyH-2)
	m.syncViewportContent()
}

func (m *model) syncViewportContent() {
	content := m.snap.Output
	if content == "" {
		content = styleMuted.Render("No command output yet.\nRun status (s), activate (a), or plugin install (i).")
	}
	m.vp.SetContent(content)
}

func (m model) maxCursor() int {
	n := 0
	switch m.tab {
	case tabSpace:
		n = len(m.snap.Layers)
	case tabProjects:
		n = len(m.snap.Projects)
	case tabFiles:
		if m.fileVerMode {
			n = len(m.fileVersions)
		} else {
			n = len(m.files)
		}
	case tabGraph:
		n = len(m.graphRows())
	case tabPlugins:
		n = len(m.snap.Plugins)
	default:
		return 0
	}
	if n <= 0 {
		return 0
	}
	return n - 1
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return styleMuted.Render("loading…")
	}

	bodyH := m.height - 4
	if m.snap.LastMsg != "" || m.snap.Err != "" || m.busy {
		bodyH = m.height - 5
	}
	if bodyH < 5 {
		bodyH = 5
	}

	body := m.renderBody(m.width, bodyH)

	flash := m.snap.LastMsg
	flashErr := false
	if m.snap.Err != "" {
		flash = m.snap.Err
		flashErr = true
	}
	busy := ""
	if m.busy {
		busy = m.spin.View() + " working"
	}

	keys := "a activate · i install · s status · ! shell · r refresh · 1–5 · j/k · ? · q"
	if m.snap.Mode == "client" {
		keys = "a activate · i install · u/U pull/push · s status · r refresh · 1–5 · j/k · ? · q"
	}
	if m.tab == tabGraph {
		if m.graphFocus != "" {
			keys = "enter walk · esc back · r re-derive · 1–6 · q"
		} else {
			keys = "enter connections · j/k move · r re-derive · 1–6 · q"
		}
	}
	if m.tab == tabFiles {
		switch {
		case m.edit.picking:
			keys = "enter open in editor · j/k choose · esc cancel"
		case m.fileVerMode:
			keys = "enter/v preview · R restore · esc back · r refresh · 1–5 · q"
		default:
			keys = "enter edit · v preview · V versions · esc back · r refresh · 1–5 · q"
		}
	}

	return Frame{
		Width:     m.width,
		Height:    m.height,
		Brand:     "ContextVerse",
		Subtitle:  fmt.Sprintf("%s · CLI wrapper", m.snap.Mode),
		RightMeta: version.Version,
		Tabs:      clientTabNames,
		ActiveTab: int(m.tab),
		Body:      body,
		Flash:     flash,
		FlashErr:  flashErr,
		BusyHint:  busy,
		Keys:      keys,
	}.Render()
}

func (m model) renderBody(w, h int) string {
	switch m.tab {
	case tabHelp:
		help := strings.Join([]string{
			"Navigation",
			"  1–5 / ?     tabs          tab        toggle focus",
			"  j/k ↑↓      move/scroll   g / G      top / bottom",
			"  pgup/pgdn   page output   esc        back",
			"",
			"Set up and inspect",
			"  s           status                  → contextd status",
			"  a           activate entry points   → contextd activate",
			"  r           refresh from disk",
			"",
			"Work on your context space (tab 3 · Files)",
			"  enter       edit selected file      → contextd file edit",
			"  v           preview (live body, or the selected version)",
			"  V           version history         → contextd file history",
			"  R           restore selected version → contextd file revert",
			"  esc         cancel picker / back to the file list",
			"",
			"Sync and storage",
			"  u / U       pull / push             → contextd pull|push",
			"              (client mode only)",
			"",
			"Deliver context to AI tools (tab 4 · Plugins)",
			"  i           wire detected clients   → contextd plugin install",
			"",
			"Move through the space (tab 6 · Graph)",
			"  enter       open a document's connections, then walk them",
			"  esc         back out to the whole graph",
			"              → contextd graph",
			"",
			"Interfaces",
			"  !           spawn $SHELL (return with exit; Model A / local)",
			"  q           quit (under Model A login → host shell)",
			"",
			"Groups match contextd --help. Every action here runs the same core the",
			"CLI does — edits go through the FileLog, so each save is a new version",
			"and never a silent write to disk. The TUI adds no capability the CLI",
			"lacks; anything missing here exists as a command.",
		}, "\n")
		return stylePane.Width(w - 2).Height(h).Render(fillHeight(help, h-2))

	case tabOutput:
		header := stylePaneTitle.Render("Command output")
		return stylePane.Width(w - 2).Height(h).Render(header + "\n" + m.vp.View())

	case tabGraph:
		return m.renderGraph(w, h)

	case tabFiles:
		return m.renderFiles(w, h)

	case tabPlugins:
		leftW := w * 42 / 100
		lines := make([]string, 0, len(m.snap.Plugins))
		for _, p := range m.snap.Plugins {
			mark := "·"
			if p.Detected {
				mark = "✓"
			}
			lines = append(lines, fmt.Sprintf("%s %-14s %s", mark, p.ID, p.Mechanism))
		}
		left := renderSelectableList(lines, m.cursor, leftW-4, h-4, "(no integrations)")
		detail := m.pluginDetail()
		return SplitTwo("Client integrations", left, "Detail", detail, w, h, 42)

	case tabProjects:
		left := renderSelectableList(m.snap.Projects, m.cursor, w*40/100-4, h-4, "(none under projects/)")
		detail := "Select a project.\n\nProjects live under projects/ in your space.\nAdd folders there, then refresh (r)."
		if len(m.snap.Projects) > 0 && m.cursor < len(m.snap.Projects) {
			name := m.snap.Projects[m.cursor]
			detail = fmt.Sprintf("Project: %s\n\nPath: %s/projects/%s\n\nTip: cd into a repo and run\n  contextd activate\nto drop entry points.", name, m.spaceRoot, name)
		}
		return SplitTwo("Projects", left, "Detail", detail, w, h, 40)

	default: // tabSpace
		layerLines := make([]string, 0, len(m.snap.Layers))
		for _, l := range m.snap.Layers {
			layerLines = append(layerLines, fmt.Sprintf("%-10s  %4d files", l.Name, l.Files))
		}
		left := renderSelectableList(layerLines, m.cursor, w*38/100-4, h-4, "(no layers)")
		detail := m.spaceDetail()
		return SplitTwo("Layers", left, "Overview", detail, w, h, 38)
	}
}

func (m model) renderFiles(w, h int) string {
	if m.filesErr != "" && len(m.files) == 0 && !m.fileVerMode {
		body := styleErr.Render(m.filesErr) + "\n\n" + styleMuted.Render("Open a space with a storage backend (config.yaml).")
		return stylePane.Width(w - 2).Height(h).Render(fillHeight(body, h-2))
	}
	if m.fileVerMode {
		labels := make([]string, 0, len(m.fileVersions))
		for _, v := range m.fileVersions {
			labels = append(labels, v.Label)
		}
		left := renderSelectableList(labels, m.cursor, w*55/100-4, h-4, "(no versions)")
		detail := fmt.Sprintf("File: %s\n\nenter/v  preview\nR        restore as current\nesc      back to files\n\nRestore appends a new version (history kept).", m.filePath)
		if m.cursor < len(m.fileVersions) {
			v := m.fileVersions[m.cursor]
			detail = fmt.Sprintf("File: %s\nSelected: v%d\n\nv preview · R restore · esc back", m.filePath, v.Version)
			if v.Destroyed {
				detail += "\n\n(destroyed — cannot restore)"
			}
			if v.Current {
				detail += "\n\n(already current)"
			}
		}
		return SplitTwo("Versions", left, "Actions", detail, w, h, 55)
	}
	leftLabels := make([]string, 0, len(m.files))
	for _, f := range m.files {
		leftLabels = append(leftLabels, f.Label)
	}
	left := renderSelectableList(leftLabels, m.cursor, w*55/100-4, h-4, "(this space has no files)")
	if m.edit.picking {
		return SplitTwo("Files", left, "Open with", m.edit.renderPicker(), w, h, 55)
	}
	detail := "Select a file, Enter to edit it.\n\nSame as Web UI / contextd file edit|list|history|revert"
	if m.cursor < len(m.files) {
		f := m.files[m.cursor]
		ver := storage.DisplayVersion(storage.Version(f.Version))
		if f.Untracked {
			ver = "not yet versioned — editing it records v1"
		}
		detail = fmt.Sprintf("File: %s\nVersion: %s\n\nenter  edit in your editor\nv      preview\nV      version history\nr      refresh list",
			f.Path, ver)
	}
	return SplitTwo("Files", left, "Detail", detail, w, h, 55)
}

func (m model) filesEnter() tea.Cmd {
	if m.fileVerMode {
		return m.filesPreview()
	}
	if m.cursor < 0 || m.cursor >= len(m.files) {
		return nil
	}
	path := m.files[m.cursor].Path
	return tea.Batch(loadFileVersionsCmd(m.spaceRoot, path), m.spin.Tick)
}

// editorPreference returns the editor remembered in this space's config, if any.
func (m model) editorPreference() string {
	cfg, err := config.Load(m.spaceRoot)
	if err != nil {
		return ""
	}
	return cfg.Editor
}

// launchEdit checks the selected file out, runs the chosen editor, and writes
// the result back through the FileLog so the edit gets its own version.
func (m *model) launchEdit() tea.Cmd {
	root := m.spaceRoot
	m.busy = true
	return m.edit.launch(
		func() (*storage.FileLog, error) { return openClientFileLog(root) },
		func(data []byte, expected storage.Version) (storage.Version, error) {
			fl, err := openClientFileLog(root)
			if err != nil {
				return "", err
			}
			return fl.Put(context.Background(), m.edit.path, data, expected)
		},
	)
}

// filesPreviewCurrent previews the live body straight from the file list. Before
// this, `v` only worked inside the version list, so pressing it on a file did
// nothing at all.
func (m model) filesPreviewCurrent() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.files) {
		return nil
	}
	path := m.files[m.cursor].Path
	root := m.spaceRoot
	return func() tea.Msg {
		fl, err := openClientFileLog(root)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		data, ver, err := fl.Get(context.Background(), path)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		body := string(data)
		if len(body) > 4000 {
			body = body[:4000] + "\n… (truncated — open with enter to see all of it)"
		}
		return actionDoneMsg{msg: fmt.Sprintf("%s @ %s (%d bytes)\n\n%s",
			path, storage.DisplayVersion(ver), len(data), body)}
	}
}

func (m model) filesPreview() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.fileVersions) {
		return nil
	}
	v := m.fileVersions[m.cursor]
	if v.Destroyed {
		return func() tea.Msg {
			return actionDoneMsg{msg: "", err: fmt.Errorf("version destroyed")}
		}
	}
	path := m.filePath
	root := m.spaceRoot
	n := v.Version
	return func() tea.Msg {
		fl, err := openClientFileLog(root)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		out, err := previewFileVersion(fl, path, n)
		return actionDoneMsg{msg: out, err: err}
	}
}

func (m model) filesRestore() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.fileVersions) {
		return nil
	}
	v := m.fileVersions[m.cursor]
	if v.Destroyed {
		return func() tea.Msg {
			return actionDoneMsg{err: fmt.Errorf("cannot restore destroyed version")}
		}
	}
	if v.Current {
		return func() tea.Msg {
			return actionDoneMsg{msg: "already current"}
		}
	}
	path := m.filePath
	root := m.spaceRoot
	n := v.Version
	return func() tea.Msg {
		fl, err := openClientFileLog(root)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		out, err := revertFileVersion(fl, root, path, n)
		return actionDoneMsg{msg: out, err: err}
	}
}

func loadFilesCmd(spaceRoot string) tea.Cmd {
	return func() tea.Msg {
		fl, err := openClientFileLog(spaceRoot)
		if err != nil {
			return filesLoadedMsg{err: err}
		}
		files, err := listSpaceFiles(fl, spaceRoot)
		return filesLoadedMsg{files: files, err: err}
	}
}

func loadFileVersionsCmd(spaceRoot, path string) tea.Cmd {
	return func() tea.Msg {
		fl, err := openClientFileLog(spaceRoot)
		if err != nil {
			return fileVersionsMsg{path: path, err: err}
		}
		_, rows, err := listVersionRows(fl, path)
		return fileVersionsMsg{path: path, versions: rows, err: err}
	}
}

func (m model) spaceDetail() string {
	// An empty directory is the first thing a new user can land on, and showing
	// them an empty shell teaches nothing. Send them to the wizard instead.
	if !m.snap.HasConfig {
		var b strings.Builder
		b.WriteString(stylePaneTitle.Render("No context space here yet"))
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "Looked in %s\n\n", m.spaceRoot)
		b.WriteString("Create one — the setup asks a few questions and explains each:\n\n")
		b.WriteString("  contextd init\n\n")
		b.WriteString(styleMuted.Render("Already have a space somewhere else?\nPoint at it with: contextd --dir <path> tui"))
		b.WriteString("\n\n")
		b.WriteString(styleMuted.Render("q quits back to your shell."))
		return b.String()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Space     %s\n", m.spaceRoot)
	fmt.Fprintf(&b, "Mode      %s\n", m.snap.Mode)
	if m.snap.IdentityName != "" {
		fmt.Fprintf(&b, "Identity  %s", m.snap.IdentityName)
		if m.snap.IdentityRole != "" {
			fmt.Fprintf(&b, " (%s)", m.snap.IdentityRole)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Projects  %d\n", len(m.snap.Projects))
	detected := 0
	for _, p := range m.snap.Plugins {
		if p.Detected {
			detected++
		}
	}
	fmt.Fprintf(&b, "Plugins   %d detected / %d known\n", detected, len(m.snap.Plugins))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("cwd  " + m.cwd))
	b.WriteString("\n\n")
	b.WriteString("Quick:\n")
	b.WriteString("  a  activate   i  install hooks\n")
	b.WriteString("  s  status     3  files tab   4  plugins tab\n")
	if m.cursor < len(m.snap.Layers) {
		l := m.snap.Layers[m.cursor]
		b.WriteString("\n")
		b.WriteString(stylePaneTitle.Render(l.Name))
		fmt.Fprintf(&b, "\n%d files under %s/%s\n", l.Files, m.spaceRoot, l.Name)
	}
	return b.String()
}

func (m model) pluginDetail() string {
	if len(m.snap.Plugins) == 0 {
		return "No client-integration templates loaded."
	}
	if m.cursor >= len(m.snap.Plugins) {
		return ""
	}
	p := m.snap.Plugins[m.cursor]
	var b strings.Builder
	fmt.Fprintf(&b, "ID         %s\n", p.ID)
	fmt.Fprintf(&b, "Display    %s\n", p.Display)
	fmt.Fprintf(&b, "Mechanism  %s\n", p.Mechanism)
	if p.Detected {
		b.WriteString(styleOk.Render("Detected  ✓  " + p.How))
		b.WriteString("\n\nPress i to install / refresh hooks for detected clients.")
	} else {
		b.WriteString(styleMuted.Render("Not detected on this machine."))
		b.WriteString("\n\nInstall still possible if you know the client is present.")
	}
	return b.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func refreshCmd(spaceRoot string) tea.Cmd {
	return func() tea.Msg {
		return refreshMsg(LoadSnapshot(spaceRoot))
	}
}

func runActionCmd(a Action, spaceRoot, cwd string) tea.Cmd {
	return func() tea.Msg {
		out, err := RunAction(a, spaceRoot, cwd)
		return actionDoneMsg{msg: out, err: err}
	}
}

// Run starts the client/solo TUI (blocking).
func Run(spaceRoot, cwd string) error {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	m := newModel(spaceRoot, cwd)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
