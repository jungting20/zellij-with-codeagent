package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/transport"
)

var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	warningStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	selectedStyle  = lipgloss.NewStyle().Reverse(true)
	activeTabStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
)

type lifecycleCounts struct {
	active   int
	problem  int
	inactive int
	unknown  int
}

func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	bodyHeight := 8
	if m.height > 0 {
		if m.height < 6 {
			bodyHeight = maxInt(1, m.height-2)
		} else {
			bodyHeight = maxInt(1, m.height-3)
		}
	}

	lines := []string{m.headerView(width)}
	if width >= 90 {
		leftWidth := maxInt(30, width*36/100)
		rightWidth := width - leftWidth - 1
		lines = append(lines, lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.runtimePanel(leftWidth, bodyHeight),
			" ",
			m.detailPanel(rightWidth, bodyHeight),
		))
	} else if m.focus == focusDetail {
		lines = append(lines, m.detailPanel(width, bodyHeight))
	} else {
		lines = append(lines, m.runtimePanel(width, bodyHeight))
	}
	if m.height > 0 && m.height < 6 {
		lines = append(lines, truncate(m.footerText(), width))
	} else {
		lines = append(lines, m.statusView(width), truncate(m.footerText(), width))
	}
	return fitScreen(strings.Join(lines, "\n"), m.width, m.height)
}

func (m Model) headerView(width int) string {
	connection := strings.ToUpper(valueOrDash(m.connection))
	marker := "~"
	style := warningStyle
	switch m.connection {
	case "live":
		marker = "*"
		style = titleStyle
	case "degraded":
		marker = "!"
		style = errorStyle
	}

	counts := lifecycleSummary(m.panes)
	parts := []string{
		titleStyle.Render("RUNTIME DASHBOARD"),
		style.Render(marker + " " + connection),
		fmt.Sprintf("%d panes", len(m.panes)),
	}
	if counts.active > 0 {
		parts = append(parts, fmt.Sprintf("active=%d", counts.active))
	}
	if counts.problem > 0 {
		parts = append(parts, fmt.Sprintf("problem=%d", counts.problem))
	}
	if counts.inactive > 0 {
		parts = append(parts, fmt.Sprintf("inactive=%d", counts.inactive))
	}
	if counts.unknown > 0 {
		parts = append(parts, fmt.Sprintf("unknown=%d", counts.unknown))
	}
	return truncate(strings.Join(parts, "  "), width)
}

func lifecycleSummary(panes []transport.Pane) lifecycleCounts {
	var counts lifecycleCounts
	for _, pane := range panes {
		switch strings.ToLower(strings.TrimSpace(pane.Status)) {
		case "starting", "running":
			counts.active++
		case "lost", "error":
			counts.problem++
		case "exited", "closed":
			counts.inactive++
		default:
			counts.unknown++
		}
	}
	return counts
}

func (m Model) runtimePanel(width, height int) string {
	bodyHeight := maxInt(1, height-3)
	allLines := m.treeLines()
	position := m.treeViewport.position(len(allLines), bodyHeight)
	title := "RUNTIME"
	if m.focus == focusTree {
		title += " [FOCUSED]"
	}
	title += "  " + position
	visible := m.treeViewport.visible(allLines, bodyHeight)
	return renderPanel(title, strings.Join(visible, "\n"), width, height, m.focus == focusTree)
}

func (m Model) treeLines() []string {
	if !m.loaded {
		return []string{"Loading runtime..."}
	}
	if len(m.rows) == 0 {
		return []string{"No managed panes", "Start a managed workspace to populate this view."}
	}
	lines := make([]string, 0, len(m.rows))
	for i, row := range m.rows {
		prefix := strings.Repeat("  ", row.depth)
		label := row.node.label
		if pane := row.node.pane; pane != nil {
			label = fmt.Sprintf("%s %s role=%s [%s]", statusSymbol(pane.Status), pane.ID, valueOrDash(pane.Role), valueOrDash(pane.Status))
		} else {
			marker := ">"
			if m.expanded[row.node.key] {
				marker = "v"
			}
			label = fmt.Sprintf("%s %s (%d)", marker, label, len(row.node.children))
		}
		line := prefix + label
		if i == m.selected {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func statusSymbol(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting":
		return "~"
	case "running":
		return "*"
	case "lost", "error":
		return "!"
	case "exited", "closed":
		return "-"
	default:
		return "?"
	}
}

func (m Model) detailPanel(width, height int) string {
	title := "DETAIL"
	if m.focus == focusDetail {
		title += " [FOCUSED]"
	}
	if pane := m.selectedPane(); pane != nil {
		title += fmt.Sprintf("  pane=%s role=%s [%s]", pane.ID, valueOrDash(pane.Role), valueOrDash(pane.Status))
	}

	bodyHeight := maxInt(1, height-4)
	var lines []string
	var position string
	tabs := activeTabStyle.Render("[Output]") + "  Events"
	if m.detailTab == tabEvents {
		tabs = "Output  " + activeTabStyle.Render("[Events]")
		lines = m.eventDisplayLines()
		position = m.eventViewport.position(len(lines), bodyHeight)
		lines = m.eventViewport.visible(lines, bodyHeight)
	} else {
		lines = m.outputLines()
		position = m.outputViewport.position(len(lines), bodyHeight)
		lines = m.outputViewport.visible(lines, bodyHeight)
		if m.snapshotting {
			lines = append([]string{"Snapshot loading..."}, lines...)
			if len(lines) > bodyHeight {
				lines = lines[:bodyHeight]
			}
		}
	}
	tabLine := tabs + "  " + mutedStyle.Render(position)
	body := tabLine + "\n" + strings.Join(lines, "\n")
	return renderPanel(title, body, width, height, m.focus == focusDetail)
}

func (m Model) eventDisplayLines() []string {
	lines := m.eventLines()
	pane := m.selectedPane()
	if pane == nil || len(m.events) == 0 {
		return lines
	}
	for i, event := range m.events {
		if event.PaneID == pane.ID {
			lines[i] = selectedStyle.Render(lines[i])
		}
	}
	return lines
}

func renderPanel(title, body string, width, height int, focused bool) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if width < 4 || height < 5 {
		return fitScreen(title+"\n"+body, width, height)
	}
	border := lipgloss.NormalBorder()
	color := lipgloss.Color("240")
	if focused {
		border = lipgloss.DoubleBorder()
		color = lipgloss.Color("42")
	}
	innerWidth := width - 2
	innerHeight := height - 2
	content := fitScreen(title+"\n"+body, innerWidth, innerHeight)
	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(color).
		Width(innerWidth).
		Height(innerHeight).
		Render(content)
}

func (m Model) statusView(width int) string {
	parts := make([]string, 0, 3)
	if m.actionText != "" && m.actionText != m.statusText {
		parts = append(parts, m.actionText)
	}
	if !m.lastRefresh.IsZero() {
		parts = append(parts, "Refreshed just now")
	}
	if m.statusText != "" {
		parts = append(parts, m.statusText)
	}
	line := strings.Join(parts, "  |  ")
	if strings.Contains(m.statusText, "failed") || m.connection == "degraded" {
		return truncate(errorStyle.Render(line), width)
	}
	return truncate(mutedStyle.Render(line), width)
}

func (m Model) footerText() string {
	if m.focus == focusDetail {
		return "j/k scroll  h/l tab  pgup/pgdn page  g/G ends  tab focus  i input  ? help  q quit"
	}
	return "j/k move  enter toggle  tab focus  s snapshot  i input  x cleanup  ? help  q quit"
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "")
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fitScreen(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	if width > 0 {
		for i, line := range lines {
			lines[i] = truncate(line, width)
		}
	}
	return strings.Join(lines, "\n")
}
