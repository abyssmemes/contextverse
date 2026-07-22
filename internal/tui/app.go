package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/abyssmemes/contextverse/internal/storage"
	"github.com/abyssmemes/contextverse/internal/version"
)

type clientTab int

const (
	tabSpace clientTab = iota
	tabProjects
	tabFiles
	tabPlugins
	tabOutput
	tabHelp
)

var clientTabNames = []string{"1 Space", "2 Projects", "3 Files", "4 Plugins", "5 Output", "? Help"}

type model struct {
	spaceRoot string
	cwd       string
	width     int
	height    int
	snap      Snapshot
	tab       clientTab
	cursor    int
	focusRight bool // Output viewport focused for scrolling
	busy      bool
	quitting  bool
	spin      spinner.Model
	vp        viewport.Model
	ready     bool

	// Files tab (version switch)
	files         []TrackedFile
	filePath      string
	fileVersions  []FileVersionRow
	fileVerMode   bool // true = browsing versions of filePath
	filesErr      string
}

type refreshMsg Snapshot
type actionDoneMsg struct {
	msg string
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
		case "esc":
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
			if m.tab == tabFiles {
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
			if m.tab == tabFiles && m.fileVerMode {
				m.busy = true
				return m, tea.Batch(m.filesPreview(), m.spin.Tick)
			}
			return m, nil
		case "up", "k":
			if m.tab == tabOutput {
				m.vp.LineUp(1)
				return m, nil
			}
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
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

	keys := "a activate · i install · s status · r refresh · 1–5 tabs · j/k · ? help · q quit"
	if m.snap.Mode == "client" {
		keys = "a activate · i install · u/U pull/push · s status · r refresh · 1–5 · j/k · ? · q"
	}
	if m.tab == tabFiles {
		keys = "enter open · R restore · v preview · esc back · r refresh · 1–5 · q"
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
			"Actions (same as CLI)",
			"  a           activate entry points + session-start hooks",
			"  i           plugin install (detected AI clients)",
			"  s           status",
			"  u / U       pull / push (client mode only)",
			"  r           refresh snapshot from disk",
			"  q           quit",
			"",
			"Files (tab 3) — version switch",
			"  enter       open versions for selected file",
			"  v           preview selected version",
			"  R           restore selected version as current",
			"  esc         back to file list",
			"",
			"Everything here wraps contextd CLI / FileLog — no extra authority.",
		}, "\n")
		return stylePane.Width(w - 2).Height(h).Render(fillHeight(help, h-2))

	case tabOutput:
		header := stylePaneTitle.Render("Command output")
		return stylePane.Width(w-2).Height(h).Render(header + "\n" + m.vp.View())

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
		return stylePane.Width(w-2).Height(h).Render(fillHeight(body, h-2))
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
	left := renderSelectableList(leftLabels, m.cursor, w*55/100-4, h-4, "(no tracked files)")
	detail := "Select a file, Enter for version history.\n\nSame as Web UI / contextd file list|history|revert"
	if m.cursor < len(m.files) {
		f := m.files[m.cursor]
		detail = fmt.Sprintf("File: %s\nVersion: %s\n\nenter  versions\nr      refresh list", f.Path, storage.DisplayVersion(storage.Version(f.Version)))
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
		files, err := listTrackedFiles(fl)
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
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Space     %s\n", m.spaceRoot))
	b.WriteString(fmt.Sprintf("Mode      %s\n", m.snap.Mode))
	if m.snap.IdentityName != "" {
		b.WriteString(fmt.Sprintf("Identity  %s", m.snap.IdentityName))
		if m.snap.IdentityRole != "" {
			b.WriteString(fmt.Sprintf(" (%s)", m.snap.IdentityRole))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("Projects  %d\n", len(m.snap.Projects)))
	detected := 0
	for _, p := range m.snap.Plugins {
		if p.Detected {
			detected++
		}
	}
	b.WriteString(fmt.Sprintf("Plugins   %d detected / %d known\n", detected, len(m.snap.Plugins)))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("cwd  " + m.cwd))
	b.WriteString("\n\n")
	b.WriteString("Quick:\n")
	b.WriteString("  a  activate   i  install hooks\n")
	b.WriteString("  s  status     3  plugins tab\n")
	if m.cursor < len(m.snap.Layers) {
		l := m.snap.Layers[m.cursor]
		b.WriteString("\n")
		b.WriteString(stylePaneTitle.Render(l.Name))
		b.WriteString(fmt.Sprintf("\n%d files under %s/%s\n", l.Files, m.spaceRoot, l.Name))
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
	b.WriteString(fmt.Sprintf("ID         %s\n", p.ID))
	b.WriteString(fmt.Sprintf("Display    %s\n", p.Display))
	b.WriteString(fmt.Sprintf("Mechanism  %s\n", p.Mechanism))
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
