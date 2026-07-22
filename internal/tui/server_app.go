package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/abyssmemes/contextverse/internal/storage"
	"github.com/abyssmemes/contextverse/internal/version"
)

type serverTab int

const (
	serverTabOverview serverTab = iota
	serverTabSpaces
	serverTabUsers
	serverTabPolicies
	serverTabBackend
	serverTabOutput
	serverTabHelp
)

var serverTabNames = []string{"1 Overview", "2 Spaces", "3 Users", "4 Policies", "5 Backend", "6 Output", "? Help"}

type serverModel struct {
	dataDir  string
	width    int
	height   int
	snap     ServerSnapshot
	tab      serverTab
	cursor   int
	busy     bool
	quitting bool
	ready    bool
	spin     spinner.Model
	vp       viewport.Model

	// Space → files → versions drill-down
	spaceName    string
	spaceFiles   []TrackedFile
	spaceFile    string
	spaceVers    []FileVersionRow
	spaceDrill   int // 0=spaces list, 1=files, 2=versions
	spaceFilesErr string
}

type spaceFilesMsg struct {
	space string
	files []TrackedFile
	err   error
}

type spaceVersionsMsg struct {
	space    string
	path     string
	versions []FileVersionRow
	err      error
}

type serverRefreshMsg ServerSnapshot
type serverActionDoneMsg struct {
	msg string
	err error
}

func newServerModel(dataDir string) serverModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)
	return serverModel{
		dataDir: dataDir,
		snap:    LoadServerSnapshot(dataDir),
		tab:     serverTabOverview,
		spin:    sp,
		vp:      viewport.New(20, 10),
	}
}

func (m serverModel) Init() tea.Cmd { return m.spin.Tick }

func (m serverModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.vp.Width = max(10, m.width-4)
		m.vp.Height = max(3, m.height-8)
		if m.snap.Output != "" {
			m.vp.SetContent(m.snap.Output)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case serverRefreshMsg:
		out, last, err := m.snap.Output, m.snap.LastMsg, m.snap.Err
		m.snap = ServerSnapshot(msg)
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
		return m, nil

	case serverActionDoneMsg:
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
		m.tab = serverTabOutput
		m.vp.Width = max(10, m.width-4)
		m.vp.Height = max(3, m.height-8)
		m.vp.SetContent(msg.msg)
		return m, tea.Batch(serverRefreshCmd(m.dataDir), m.spin.Tick)

	case spaceFilesMsg:
		m.busy = false
		if msg.err != nil {
			m.spaceFilesErr = msg.err.Error()
			m.spaceFiles = nil
			return m, nil
		}
		m.spaceName = msg.space
		m.spaceFiles = msg.files
		m.spaceDrill = 1
		m.spaceFile = ""
		m.spaceVers = nil
		m.spaceFilesErr = ""
		m.cursor = 0
		return m, nil

	case spaceVersionsMsg:
		m.busy = false
		if msg.err != nil {
			m.spaceFilesErr = msg.err.Error()
			return m, nil
		}
		m.spaceName = msg.space
		m.spaceFile = msg.path
		m.spaceVers = msg.versions
		m.spaceDrill = 2
		m.spaceFilesErr = ""
		m.cursor = 0
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
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			m.busy = true
			return m, tea.Batch(serverRefreshCmd(m.dataDir), m.spin.Tick)
		case "?":
			m.tab = serverTabHelp
			return m, nil
		case "esc", "h":
			if m.tab == serverTabSpaces && m.spaceDrill > 0 {
				m.spaceDrill--
				m.cursor = 0
				if m.spaceDrill == 0 {
					m.spaceName = ""
					m.spaceFiles = nil
					m.spaceFile = ""
					m.spaceVers = nil
				}
				if m.spaceDrill == 1 {
					m.spaceFile = ""
					m.spaceVers = nil
				}
				return m, nil
			}
			m.tab = serverTabOverview
			m.cursor = 0
			m.spaceDrill = 0
			return m, nil
		case "1":
			m.tab = serverTabOverview
			m.cursor = 0
			m.spaceDrill = 0
			return m, nil
		case "2", "S":
			m.tab = serverTabSpaces
			m.cursor = 0
			m.spaceDrill = 0
			return m, nil
		case "3", "u":
			m.tab = serverTabUsers
			m.cursor = 0
			return m, nil
		case "4", "p":
			m.tab = serverTabPolicies
			m.cursor = 0
			return m, nil
		case "5", "b":
			m.tab = serverTabBackend
			m.cursor = 0
			return m, nil
		case "6", "o":
			m.tab = serverTabOutput
			if m.snap.Output != "" {
				m.vp.SetContent(m.snap.Output)
			}
			return m, nil
		case "s":
			m.busy = true
			return m, tea.Batch(runServerActionCmd(ServerActionStatus, m.dataDir), m.spin.Tick)
		case "H":
			m.busy = true
			return m, tea.Batch(runServerActionCmd(ServerActionHealth, m.dataDir), m.spin.Tick)
		case "enter":
			if m.tab == serverTabSpaces {
				m.busy = true
				return m, tea.Batch(m.spaceEnter(), m.spin.Tick)
			}
			return m, nil
		case "R":
			if m.tab == serverTabSpaces && m.spaceDrill == 2 {
				m.busy = true
				return m, tea.Batch(m.spaceRestore(), m.spin.Tick)
			}
			return m, nil
		case "v":
			if m.tab == serverTabSpaces && m.spaceDrill == 2 {
				m.busy = true
				return m, tea.Batch(m.spacePreview(), m.spin.Tick)
			}
			return m, nil
		case "up", "k":
			if m.tab == serverTabOutput {
				m.vp.LineUp(1)
				return m, nil
			}
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.tab == serverTabOutput {
				m.vp.LineDown(1)
				return m, nil
			}
			if m.cursor < m.maxCursor() {
				m.cursor++
			}
			return m, nil
		case "pgup", "ctrl+u":
			m.vp.HalfViewUp()
			return m, nil
		case "pgdown", "ctrl+d":
			m.vp.HalfViewDown()
			return m, nil
		case "g":
			if m.tab == serverTabOutput {
				m.vp.GotoTop()
			} else {
				m.cursor = 0
			}
			return m, nil
		case "G":
			if m.tab == serverTabOutput {
				m.vp.GotoBottom()
			} else {
				m.cursor = m.maxCursor()
			}
			return m, nil
		}
	}
	return m, nil
}

func (m serverModel) maxCursor() int {
	var n int
	switch m.tab {
	case serverTabSpaces:
		switch m.spaceDrill {
		case 1:
			n = len(m.spaceFiles)
		case 2:
			n = len(m.spaceVers)
		default:
			n = len(m.snap.Spaces)
		}
	case serverTabUsers:
		n = len(m.snap.Users)
	case serverTabPolicies:
		n = len(m.snap.Policies)
	default:
		return 0
	}
	if n <= 0 {
		return 0
	}
	return n - 1
}

func (m serverModel) View() string {
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

	sub := "server admin · CLI wrapper"
	if !m.snap.Exists {
		sub = "server not initialized"
	}

	return Frame{
		Width:     m.width,
		Height:    m.height,
		Brand:     "ContextVerse",
		Subtitle:  sub,
		RightMeta: version.Version,
		Tabs:      serverTabNames,
		ActiveTab: int(m.tab),
		Body:      m.renderBody(m.width, bodyH),
		Flash:     flash,
		FlashErr:  flashErr,
		BusyHint:  busy,
		Keys: func() string {
			if m.tab == serverTabSpaces && m.spaceDrill > 0 {
				return "enter open · R restore · v preview · esc back · j/k · q"
			}
			return "1–6 tabs · s status · H health · r refresh · j/k · ? help · q quit"
		}(),
	}.Render()
}

func (m serverModel) renderBody(w, h int) string {
	if !m.snap.Exists {
		msg := styleErr.Render("Server not initialized.") + "\n\n" +
			styleMuted.Render("Run: contextd init server") + "\n" +
			styleMuted.Render("Then: contextd tui --server")
		return stylePane.Width(w-2).Height(h).Render(fillHeight(msg, h-2))
	}

	switch m.tab {
	case serverTabHelp:
		help := strings.Join([]string{
			"Navigation",
			"  1–6 / ?     tabs          esc/h      overview",
			"  j/k ↑↓      move/scroll   g / G      top / bottom",
			"",
			"Actions (CLI wrappers)",
			"  s           server status",
			"  H           health probe",
			"  r           refresh from data dir",
			"  q           quit",
			"",
			"Wish SSH (Model B): contextd tui ssh enable && server start",
			"No UI-only ops — same verbs as the CLI.",
		}, "\n")
		return stylePane.Width(w-2).Height(h).Render(fillHeight(help, h-2))

	case serverTabOutput:
		content := m.snap.Output
		if content == "" {
			content = styleMuted.Render("No output yet. Press s (status) or H (health).")
		}
		// viewport content set in Update; re-apply empty placeholder via local view
		view := m.vp.View()
		if m.snap.Output == "" {
			view = content
		}
		return stylePane.Width(w-2).Height(h).Render(stylePaneTitle.Render("Command output") + "\n" + view)

	case serverTabBackend:
		body := fmt.Sprintf("Driver / target\n\n%s\n\n%s",
			m.snap.Backend,
			styleMuted.Render("Change via: contextd backend …  or admin Web UI"))
		return stylePane.Width(w-2).Height(h).Render(fillHeight(body, h-2))

	case serverTabSpaces:
		return m.renderSpaceDrill(w, h)

	case serverTabUsers:
		left := renderSelectableList(m.snap.Users, m.cursor, w*48/100-4, h-4, "(no users)")
		detail := "Select a user.\n\nFormat: name\\tpolicies"
		if len(m.snap.Users) > 0 && m.cursor < len(m.snap.Users) {
			detail = m.snap.Users[m.cursor] + "\n\nManage: contextd user …"
		}
		return SplitTwo("Users", left, "Detail", detail, w, h, 48)

	case serverTabPolicies:
		left := renderSelectableList(m.snap.Policies, m.cursor, w*40/100-4, h-4, "(no policies)")
		detail := "Select a policy."
		if len(m.snap.Policies) > 0 && m.cursor < len(m.snap.Policies) {
			detail = fmt.Sprintf("Policy: %s\n\nShow: contextd policy show %s",
				m.snap.Policies[m.cursor], m.snap.Policies[m.cursor])
		}
		return SplitTwo("Policies", left, "Detail", detail, w, h, 40)

	default:
		cards := []string{
			fmt.Sprintf("listen     %s", m.snap.Listen),
			fmt.Sprintf("backend    %s", m.snap.Backend),
			fmt.Sprintf("default    %s", m.snap.Space),
			fmt.Sprintf("data dir   %s", m.dataDir),
			"",
			fmt.Sprintf("spaces     %d", len(m.snap.Spaces)),
			fmt.Sprintf("users      %d", len(m.snap.Users)),
			fmt.Sprintf("policies   %d", len(m.snap.Policies)),
			"",
			styleMuted.Render("s status · H health · 2 spaces · 3 users · 4 policies"),
		}
		return stylePane.Width(w-2).Height(h).Render(
			stylePaneTitle.Render("Overview") + "\n" + fillHeight(strings.Join(cards, "\n"), h-3),
		)
	}
}

func (m serverModel) renderSpaceDrill(w, h int) string {
	switch m.spaceDrill {
	case 1:
		if m.spaceFilesErr != "" && len(m.spaceFiles) == 0 {
			body := styleErr.Render(m.spaceFilesErr)
			return stylePane.Width(w-2).Height(h).Render(fillHeight(body, h-2))
		}
		labels := make([]string, 0, len(m.spaceFiles))
		for _, f := range m.spaceFiles {
			labels = append(labels, f.Label)
		}
		left := renderSelectableList(labels, m.cursor, w*55/100-4, h-4, "(no tracked files)")
		detail := fmt.Sprintf("Space: %s\n\nenter  versions\nesc    back", m.spaceName)
		if m.cursor < len(m.spaceFiles) {
			f := m.spaceFiles[m.cursor]
			detail = fmt.Sprintf("Space: %s\nFile: %s\nVersion: %s\n\nenter versions · esc back",
				m.spaceName, f.Path, storage.DisplayVersion(storage.Version(f.Version)))
		}
		return SplitTwo("Files", left, "Detail", detail, w, h, 55)
	case 2:
		labels := make([]string, 0, len(m.spaceVers))
		for _, v := range m.spaceVers {
			labels = append(labels, v.Label)
		}
		left := renderSelectableList(labels, m.cursor, w*55/100-4, h-4, "(no versions)")
		detail := fmt.Sprintf("%s / %s\n\nv preview · R restore · esc back", m.spaceName, m.spaceFile)
		if m.cursor < len(m.spaceVers) {
			v := m.spaceVers[m.cursor]
			detail = fmt.Sprintf("%s / %s\nv%d\n\nv preview · R restore · esc back", m.spaceName, m.spaceFile, v.Version)
		}
		return SplitTwo("Versions", left, "Actions", detail, w, h, 55)
	default:
		left := renderSelectableList(m.snap.Spaces, m.cursor, w*40/100-4, h-4, "(no spaces)")
		detail := "Select a space, Enter to browse files / versions."
		if len(m.snap.Spaces) > 0 && m.cursor < len(m.snap.Spaces) {
			name := m.snap.Spaces[m.cursor]
			detail = fmt.Sprintf("Space: %s\n\nPath: %s/spaces/%s\n\nenter  browse files\n(same FileLog as Web UI)", name, m.dataDir, name)
		}
		return SplitTwo("Spaces", left, "Detail", detail, w, h, 40)
	}
}

func (m serverModel) spaceEnter() tea.Cmd {
	switch m.spaceDrill {
	case 0:
		if m.cursor < 0 || m.cursor >= len(m.snap.Spaces) {
			return nil
		}
		space := m.snap.Spaces[m.cursor]
		dir := m.dataDir
		return func() tea.Msg {
			fl, err := openServerSpaceFileLog(dir, space)
			if err != nil {
				return spaceFilesMsg{space: space, err: err}
			}
			files, err := listTrackedFiles(fl)
			return spaceFilesMsg{space: space, files: files, err: err}
		}
	case 1:
		if m.cursor < 0 || m.cursor >= len(m.spaceFiles) {
			return nil
		}
		path := m.spaceFiles[m.cursor].Path
		space := m.spaceName
		dir := m.dataDir
		return func() tea.Msg {
			fl, err := openServerSpaceFileLog(dir, space)
			if err != nil {
				return spaceVersionsMsg{space: space, path: path, err: err}
			}
			_, rows, err := listVersionRows(fl, path)
			return spaceVersionsMsg{space: space, path: path, versions: rows, err: err}
		}
	case 2:
		return m.spacePreview()
	default:
		return nil
	}
}

func (m serverModel) spacePreview() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.spaceVers) {
		return nil
	}
	v := m.spaceVers[m.cursor]
	if v.Destroyed {
		return func() tea.Msg { return serverActionDoneMsg{err: fmt.Errorf("version destroyed")} }
	}
	space, path, n, dir := m.spaceName, m.spaceFile, v.Version, m.dataDir
	return func() tea.Msg {
		fl, err := openServerSpaceFileLog(dir, space)
		if err != nil {
			return serverActionDoneMsg{err: err}
		}
		out, err := previewFileVersion(fl, path, n)
		return serverActionDoneMsg{msg: out, err: err}
	}
}

func (m serverModel) spaceRestore() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.spaceVers) {
		return nil
	}
	v := m.spaceVers[m.cursor]
	if v.Destroyed {
		return func() tea.Msg { return serverActionDoneMsg{err: fmt.Errorf("cannot restore destroyed version")} }
	}
	if v.Current {
		return func() tea.Msg { return serverActionDoneMsg{msg: "already current"} }
	}
	space, path, n, dir := m.spaceName, m.spaceFile, v.Version, m.dataDir
	return func() tea.Msg {
		fl, err := openServerSpaceFileLog(dir, space)
		if err != nil {
			return serverActionDoneMsg{err: err}
		}
		out, err := revertFileVersion(fl, filepath.Join(dir, "spaces", space), path, n)
		return serverActionDoneMsg{msg: out, err: err}
	}
}

func serverRefreshCmd(dataDir string) tea.Cmd {
	return func() tea.Msg {
		return serverRefreshMsg(LoadServerSnapshot(dataDir))
	}
}

func runServerActionCmd(a ServerAction, dataDir string) tea.Cmd {
	return func() tea.Msg {
		out, err := RunServerAction(a, dataDir)
		return serverActionDoneMsg{msg: out, err: err}
	}
}

// RunServer starts the server admin TUI (blocking).
func RunServer(dataDir string) error {
	m := newServerModel(dataDir)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
