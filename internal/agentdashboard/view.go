package agentdashboard

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/transport"
)

var (
	titleStyle           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	workingStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	blockedStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	idleStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	unknownStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	selectedStyle        = lipgloss.NewStyle().Reverse(true)
	sessionStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	tabStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	mutedStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	pinnedSectionStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	unpinnedSectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	errorStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.aliasTarget != "" {
		return m.aliasView()
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	lines := []string{m.headerView()}
	activityLines := m.activityView()
	bodyHeight := maxInt(3, len(m.displayRows())+2)
	if m.height > 0 {
		bodyHeight = maxInt(1, m.height-3-len(activityLines))
	}
	if width >= 100 {
		leftWidth := (width - 3) * 35 / 100
		rightWidth := width - 3 - leftWidth
		left := m.panelView(true, leftWidth, bodyHeight)
		right := m.panelView(false, rightWidth, bodyHeight)
		for index := 0; index < bodyHeight; index++ {
			lines = append(lines, padCell(left[index], leftWidth)+" │ "+padCell(right[index], rightWidth))
		}
	} else {
		lines = append(lines, m.panelView(m.focusPinned, width, bodyHeight)...)
	}
	lines = append(lines, activityLines...)
	if m.statusText != "" {
		style := mutedStyle
		if m.connection == "degraded" || strings.Contains(m.statusText, "failed") {
			style = errorStyle
		}
		lines = append(lines, style.Render(m.statusText))
	}
	lines = append(lines, "Tab j/k i input g lazygit a alias Space pin d close Enter focus R refresh q quit")
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "…")
	}
	if m.height > 0 && len(lines) > m.height {
		lines = append(lines[:m.height-1], lines[len(lines)-1])
	}
	base := strings.Join(lines, "\n")
	if m.inputPane != "" {
		return m.inputOverlay(base)
	}
	return base
}

// Each area has its own viewport, derived from its remembered selection.
func (m Model) panelView(pinned bool, width, height int) []string {
	kind := displayUnpinned
	if pinned {
		kind = displayPinned
	}
	heading := sectionView(displayRow{kind: kind, count: len(m.panelIndices(pinned))}, width)
	if pinned == m.focusPinned {
		heading = selectedStyle.Render(ansi.Truncate(ansi.Strip(heading), width, "…"))
	}
	lines := []string{heading}
	if height > 1 {
		lines = append(lines, "PIN  PROJECT  STATE  AGENT  SINCE")
	}
	var rows []displayRow
	inPanel := false
	for _, row := range m.displayRows() {
		if row.isSection() {
			inPanel = row.kind == kind
			continue
		}
		if inPanel {
			rows = append(rows, row)
		}
	}
	selected := -1
	selection := m.selections[panelIndex(pinned)]
	indices := m.panelIndices(pinned)
	numbers := make(map[int]int)
	for _, index := range indices {
		if index >= 9 {
			break
		}
		numbers[index] = index + 1
	}
	if pinned == m.focusPinned && len(indices) > 0 {
		selected = m.selected
	} else if len(indices) > 0 {
		selected = indices[minInt(selection.index, len(indices)-1)]
	}
	visible := maxInt(0, height-len(lines))
	start := viewportStart(displayIndexForAgent(rows, selected), len(rows), visible)
	if !m.loaded {
		if visible > 0 {
			lines = append(lines, "Loading agents...")
		}
	} else {
		for index := start; index < len(rows) && index < start+visible; index++ {
			row := rows[index]
			switch row.kind {
			case displayEmpty:
				lines = append(lines, mutedStyle.Render("    No agents"))
			case displaySession:
				lines = append(lines, m.sessionView(row.session, row.count))
			case displayTab:
				lines = append(lines, m.tabView(row.tab, row.count))
			default:
				lines = append(lines, m.rowView(m.rows[row.agentIndex], pinned == m.focusPinned && row.agentIndex == selected, width, numbers[row.agentIndex]))
			}
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "…")
	}
	return lines
}

type displayRow struct {
	kind       displayRowKind
	session    string
	tab        string
	count      int
	agentIndex int
}

type displayRowKind uint8

const (
	displayAgent displayRowKind = iota
	displayPinned
	displayUnpinned
	displayEmpty
	displaySession
	displayTab
)

func (row displayRow) isSection() bool {
	return row.kind == displayPinned || row.kind == displayUnpinned
}

func sectionView(row displayRow, width int) string {
	label, style := "PINNED", pinnedSectionStyle
	if row.kind == displayUnpinned {
		label, style = "UNPINNED", unpinnedSectionStyle
	}
	heading := fmt.Sprintf("── %s (%d) ", label, row.count)
	return style.Render(heading + strings.Repeat("─", maxInt(0, width-ansi.StringWidth(heading))))
}

func (m Model) displayRows() []displayRow {
	rows := make([]displayRow, 0, len(m.rows)*3)
	start := 0
	for start < len(m.rows) && m.rows[start].Agent.Pinned {
		start++
	}
	rows = append(rows, displayRow{kind: displayPinned, count: start})
	if start == 0 {
		rows = append(rows, displayRow{kind: displayEmpty})
	}
	for index := 0; index < start; index++ {
		rows = append(rows, displayRow{kind: displayAgent, agentIndex: index})
	}
	rows = append(rows, displayRow{kind: displayUnpinned, count: len(m.rows) - start})
	if start == len(m.rows) {
		rows = append(rows, displayRow{kind: displayEmpty})
	}
	for start < len(m.rows) {
		session := sessionName(m.rows[start])
		sessionEnd := start + 1
		for sessionEnd < len(m.rows) && sessionName(m.rows[sessionEnd]) == session {
			sessionEnd++
		}
		rows = append(rows, displayRow{kind: displaySession, session: session, count: sessionEnd - start})
		for tabStart := start; tabStart < sessionEnd; {
			tab := tabKey(m.rows[tabStart])
			tabEnd := tabStart + 1
			for tabEnd < sessionEnd && tabKey(m.rows[tabEnd]) == tab {
				tabEnd++
			}
			rows = append(rows, displayRow{kind: displayTab, tab: tabName(m.rows[tabStart]), count: tabEnd - tabStart})
			for index := tabStart; index < tabEnd; index++ {
				rows = append(rows, displayRow{kind: displayAgent, agentIndex: index})
			}
			tabStart = tabEnd
		}
		start = sessionEnd
	}
	return rows
}

func displayIndexForAgent(rows []displayRow, agentIndex int) int {
	for index, row := range rows {
		if row.kind == displayAgent && row.agentIndex == agentIndex {
			return index
		}
	}
	return 0
}

func (m Model) sessionView(session string, count int) string {
	label := fmt.Sprintf("%s (%d)", session, count)
	if strings.TrimSpace(m.opts.SourceSession) == session {
		label += "  current"
	}
	return sessionStyle.Render(label)
}

func (m Model) tabView(tab string, count int) string {
	return tabStyle.Render(fmt.Sprintf("  %s (%d)", tab, count))
}

func (m Model) headerView() string {
	marker, connection := "~", strings.ToUpper(m.connection)
	style := unknownStyle
	switch m.connection {
	case "live":
		marker, style = "*", workingStyle
	case "degraded":
		marker, style = "!", errorStyle
	}
	if connection == "" {
		connection = "CONNECTING"
	}
	return fmt.Sprintf("%s  %s  %d agents",
		titleStyle.Render("AGENT DASHBOARD"),
		style.Render(marker+" "+connection),
		len(m.rows),
	)
}

func (m Model) rowView(record transport.AgentWithPane, selected bool, width, number int) string {
	now := m.lastRefresh
	if now.IsZero() {
		now = time.Now()
	}
	projectWidth := maxInt(8, width-39)
	pin := " "
	if record.Agent.Pinned {
		pin = "*"
	}
	prefix := "    "
	if number > 0 {
		prefix = fmt.Sprintf("  %d ", number)
	}
	if selected {
		prefix = ">" + prefix[1:]
	}
	project := projectName(record.Pane.CWD)
	if record.Agent.TaskAlias != "" {
		badge := "[" + codingagent.TaskAlias(record.Agent.TaskAlias).Label() + "]"
		project = badge + " " + project
		projectWidth = maxInt(projectWidth, ansi.StringWidth(badge)+9)
	}
	line := prefix + pin + " " + padCell(project, projectWidth) +
		"  " + padCell(stateView(record.Agent.State), 10) +
		"  " + padCell(agentName(record.Agent.Kind), 12) +
		"  " + elapsed(now, record.Agent.StateChangedAt)
	if selected {
		return selectedStyle.Render(line)
	}
	return line
}

func stateView(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "working":
		return workingStyle.Render("● working")
	case "blocked":
		return blockedStyle.Render("! blocked")
	case "idle":
		return idleStyle.Render("○ idle")
	default:
		return unknownStyle.Render("? unknown")
	}
}

func agentName(kind string) string {
	if profile, ok := codingagent.LookupProfile(codingagent.Kind(kind)); ok {
		return profile.DisplayName
	}
	if strings.TrimSpace(kind) == "" {
		return "Unknown"
	}
	return kind
}

func projectName(cwd string) string {
	name := filepath.Base(filepath.Clean(cwd))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "-"
	}
	return name
}

func elapsed(now, changed time.Time) string {
	if changed.IsZero() {
		return "--:--"
	}
	duration := now.Sub(changed)
	if duration < 0 {
		duration = 0
	}
	duration = duration.Truncate(time.Second)
	if duration < time.Hour {
		return fmt.Sprintf("%02d:%02d", int(duration/time.Minute), int(duration/time.Second)%60)
	}
	return fmt.Sprintf("%02d:%02d", int(duration/time.Hour), int(duration/time.Minute)%60)
}

func padCell(value string, width int) string {
	value = ansi.Truncate(value, width, "…")
	return value + strings.Repeat(" ", maxInt(0, width-ansi.StringWidth(value)))
}

func viewportStart(selected, count, visible int) int {
	if count <= visible || visible <= 0 {
		return 0
	}
	start := selected - visible + 1
	if start < 0 {
		return 0
	}
	if start > count-visible {
		return count - visible
	}
	return start
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
