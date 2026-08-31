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
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	workingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	blockedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	idleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	unknownStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	sessionStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	tabStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	lines := []string{m.headerView(), "STATE  AGENT  ACCESS  PROJECT  SINCE"}
	if !m.loaded {
		lines = append(lines, "Loading agents...")
	} else if len(m.rows) == 0 {
		lines = append(lines, "No managed coding agents")
	} else {
		displayRows := m.displayRows()
		visible := len(displayRows)
		if m.height > 0 {
			visible = minInt(visible, maxInt(1, m.height-4))
		}
		selected := displayIndexForAgent(displayRows, m.selected)
		start := viewportStart(selected, len(displayRows), visible)
		for index := start; index < len(displayRows) && index < start+visible; index++ {
			row := displayRows[index]
			switch row.kind {
			case displaySession:
				lines = append(lines, m.sessionView(row.session, row.count))
				continue
			case displayTab:
				lines = append(lines, m.tabView(row.tab, row.count))
				continue
			}
			lines = append(lines, m.rowView(m.rows[row.agentIndex], row.agentIndex == m.selected, width))
		}
	}
	if m.statusText != "" {
		style := mutedStyle
		if m.connection == "degraded" || strings.Contains(m.statusText, "failed") {
			style = errorStyle
		}
		lines = append(lines, style.Render(m.statusText))
	}
	lines = append(lines, "j/k/Tab/S-Tab move  Enter focus  R refresh  q quit")
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "…")
	}
	if m.height > 0 && len(lines) > m.height {
		lines = append(lines[:m.height-1], lines[len(lines)-1])
	}
	return strings.Join(lines, "\n")
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
	displaySession
	displayTab
)

func (m Model) displayRows() []displayRow {
	rows := make([]displayRow, 0, len(m.rows)*3)
	for start := 0; start < len(m.rows); {
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
		if row.agentIndex == agentIndex {
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

func (m Model) rowView(record transport.AgentWithPane, selected bool, width int) string {
	now := m.lastRefresh
	if now.IsZero() {
		now = time.Now()
	}
	projectWidth := maxInt(8, width-48)
	line := "    " + padCell(stateView(record.Agent.State), 10) +
		"  " + padCell(agentName(record.Agent.Kind), 12) +
		"  " + padCell(accessName(record.Agent.Access), 9) +
		"  " + padCell(projectName(record.Pane.CWD), projectWidth) +
		"  " + elapsed(now, record.Agent.StateChangedAt)
	if selected {
		line = "  > " + strings.TrimPrefix(line, "    ")
		return selectedStyle.Render(line)
	}
	return line
}

func accessName(access string) string {
	if strings.TrimSpace(access) == "" {
		return "full"
	}
	return access
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
