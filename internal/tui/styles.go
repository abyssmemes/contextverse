package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette — teal/ink, readable on dark or light terminals.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#0f7a6c", Dark: "#2dd4bf"}
	colInk    = lipgloss.AdaptiveColor{Light: "#0b1f33", Dark: "#e8eef4"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#5a7085", Dark: "#8aa0b4"}
	colPanel  = lipgloss.AdaptiveColor{Light: "#e8eef3", Dark: "#122033"}
	colBorder = lipgloss.AdaptiveColor{Light: "#b7c5d3", Dark: "#2a3f55"}
	colDanger = lipgloss.AdaptiveColor{Light: "#b42318", Dark: "#f87171"}
	colOk     = lipgloss.AdaptiveColor{Light: "#1a7f4b", Dark: "#4ade80"}
	colTabOn  = lipgloss.AdaptiveColor{Light: "#0f7a6c", Dark: "#0f7a6c"}
	colTabFg  = lipgloss.AdaptiveColor{Light: "#f3f6f9", Dark: "#f3f6f9"}
)

var (
	styleBrand = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(colInk)
	styleMuted = lipgloss.NewStyle().Foreground(colMuted)
	styleSel   = lipgloss.NewStyle().Foreground(colTabFg).Background(colAccent).Padding(0, 1)
	styleItem  = lipgloss.NewStyle().Foreground(colInk).Padding(0, 1)
	styleOk    = lipgloss.NewStyle().Foreground(colOk)
	styleErr   = lipgloss.NewStyle().Foreground(colDanger)
	styleBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).Padding(0, 1)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colInk).
			Background(colPanel).
			Padding(0, 1)

	styleFooter = lipgloss.NewStyle().
			Foreground(colMuted).
			Background(colPanel).
			Padding(0, 1)

	styleTabOn = lipgloss.NewStyle().
			Bold(true).
			Foreground(colTabFg).
			Background(colTabOn).
			Padding(0, 2)

	styleTabOff = lipgloss.NewStyle().
			Foreground(colMuted).
			Padding(0, 2)

	stylePane = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	stylePaneTitle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleFlashOK   = lipgloss.NewStyle().Foreground(colOk).Background(colPanel).Padding(0, 1)
	styleFlashErr  = lipgloss.NewStyle().Foreground(colDanger).Background(colPanel).Padding(0, 1)
)

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " · ")
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := lipgloss.Width(s)
	if r >= w {
		return lipgloss.NewStyle().MaxWidth(w).Render(s)
	}
	return s + strings.Repeat(" ", w-r)
}

func fillHeight(content string, height int) string {
	if height <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		return strings.Join(lines[:height], "\n")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// Frame is the full-screen chrome: header + tabs + body + flash + footer.
type Frame struct {
	Width, Height int
	Brand         string
	Subtitle      string
	RightMeta     string
	Tabs          []string
	ActiveTab     int
	Body          string
	Flash         string
	FlashErr      bool
	BusyHint      string
	Keys          string
}

func (f Frame) Render() string {
	w := f.Width
	h := f.Height
	if w < 40 {
		w = 40
	}
	if h < 12 {
		h = 12
	}

	left := styleBrand.Render(f.Brand)
	if f.Subtitle != "" {
		left += "  " + styleMuted.Render(f.Subtitle)
	}
	right := styleMuted.Render(f.RightMeta)
	gap := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	headerInner := left + strings.Repeat(" ", gap) + right
	header := styleHeader.Width(w).Render(padRight(headerInner, w-2))

	var tabParts []string
	for i, t := range f.Tabs {
		if i == f.ActiveTab {
			tabParts = append(tabParts, styleTabOn.Render(t))
		} else {
			tabParts = append(tabParts, styleTabOff.Render(t))
		}
	}
	tabs := lipgloss.NewStyle().Width(w).Background(colPanel).Render(
		padRight(strings.Join(tabParts, ""), w),
	)

	flash := ""
	if f.BusyHint != "" {
		flash = styleMuted.Width(w).Background(colPanel).Padding(0, 1).Render(padRight("… "+f.BusyHint, w-2))
	} else if f.Flash != "" {
		msg := truncate(f.Flash, w-4)
		if f.FlashErr {
			flash = styleFlashErr.Width(w).Render(padRight(msg, w-2))
		} else {
			flash = styleFlashOK.Width(w).Render(padRight(msg, w-2))
		}
	}

	footer := styleFooter.Width(w).Render(padRight(truncate(f.Keys, w-2), w-2))

	chrome := 1 + 1 + 1 // header, tabs, footer
	if flash != "" {
		chrome++
	}
	bodyH := h - chrome
	if bodyH < 3 {
		bodyH = 3
	}
	body := lipgloss.NewStyle().Width(w).Height(bodyH).MaxHeight(bodyH).Render(fillHeight(f.Body, bodyH))

	parts := []string{header, tabs, body}
	if flash != "" {
		parts = append(parts, flash)
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// SplitTwo renders a left|right pane pair filling width×height.
func SplitTwo(leftTitle, leftBody, rightTitle, rightBody string, width, height, leftRatio int) string {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	if leftRatio <= 0 || leftRatio >= 90 {
		leftRatio = 36
	}
	lw := width * leftRatio / 100
	if lw < 18 {
		lw = 18
	}
	if lw > width-24 {
		lw = width - 24
		if lw < 10 {
			lw = width / 2
		}
	}
	rw := width - lw
	innerH := height - 2 // borders
	if innerH < 1 {
		innerH = 1
	}

	left := stylePane.Width(lw - 2).Height(height).
		Render(stylePaneTitle.Render(leftTitle) + "\n" + fillHeight(leftBody, innerH-1))
	right := stylePane.Width(rw - 2).Height(height).
		Render(stylePaneTitle.Render(rightTitle) + "\n" + fillHeight(rightBody, innerH-1))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func renderSelectableList(items []string, cursor, width, maxLines int, empty string) string {
	if maxLines < 1 {
		maxLines = 1
	}
	if len(items) == 0 {
		return styleMuted.Render(empty)
	}
	start := 0
	if cursor >= maxLines {
		start = cursor - maxLines + 1
	}
	end := start + maxLines
	if end > len(items) {
		end = len(items)
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		line := truncate(items[i], width-4)
		if i == cursor {
			b.WriteString(styleSel.Width(width - 2).Render(line))
		} else {
			b.WriteString(styleItem.Width(width - 2).Render(line))
		}
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if start > 0 || end < len(items) {
		b.WriteString("\n")
		b.WriteString(styleMuted.Render(truncate(fmtScroll(start+1, end, len(items)), width-2)))
	}
	return b.String()
}

func fmtScroll(from, to, total int) string {
	return fmt.Sprintf("┄ %d–%d / %d", from, to, total)
}
