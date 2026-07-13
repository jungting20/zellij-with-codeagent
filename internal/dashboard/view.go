package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	selectedStyle = lipgloss.NewStyle().Reverse(true)
)

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("RUNTIME DASHBOARD"))
	b.WriteString("  connection=")
	b.WriteString(m.connection)
	b.WriteByte('\n')

	tree := m.treeView()
	detail := m.detailView()
	if m.width >= 80 {
		leftWidth := maxInt(32, m.width*42/100)
		rightWidth := maxInt(30, m.width-leftWidth-1)
		left := renderBlock("RUNTIME", tree, leftWidth)
		right := renderBlock("OUTPUT / EVENTS", detail, rightWidth)
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right))
	} else {
		width := m.width
		if width <= 0 {
			width = 80
		}
		b.WriteString(renderBlock("RUNTIME", tree, width))
		b.WriteByte('\n')
		b.WriteString(renderBlock("OUTPUT / EVENTS", detail, width))
	}
	b.WriteByte('\n')
	if strings.Contains(m.statusText, "failed") || m.connection == "degraded" {
		b.WriteString(errorStyle.Render(m.statusText))
	} else {
		b.WriteString(mutedStyle.Render(m.statusText))
	}
	b.WriteByte('\n')
	b.WriteString(mutedStyle.Render("j/k: move  enter: expand  s: snapshot  i: input  r: reconcile  x: cleanup  R: refresh  q: quit"))
	return b.String()
}

func (m Model) treeView() string {
	if len(m.rows) == 0 {
		return "no managed panes"
	}
	var lines []string
	for i, row := range m.rows {
		prefix := strings.Repeat("  ", row.depth)
		if row.node.kind != "pane" {
			marker := "+"
			if m.expanded[row.node.key] {
				marker = "-"
			}
			prefix += marker + " "
		}
		label := row.node.label
		if pane := row.node.pane; pane != nil {
			label = fmt.Sprintf("%s role=%s [%s]", pane.ID, valueOrDash(pane.Role), valueOrDash(pane.Status))
		}
		line := prefix + label
		if i == m.selected {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) detailView() string {
	var b strings.Builder
	pane := m.selectedPane()
	if pane == nil {
		b.WriteString("select a pane to inspect output")
	} else {
		fmt.Fprintf(&b, "pane=%s role=%s [%s]\n", pane.ID, valueOrDash(pane.Role), valueOrDash(pane.Status))
		if m.snapshotting {
			b.WriteString("snapshot loading...\n")
		}
		if output, ok := m.snapshots[pane.ID]; ok && output != "" {
			b.WriteString(output)
		} else {
			b.WriteString("no snapshot output")
		}
	}
	b.WriteString("\n\nEVENTS\n")
	if len(m.events) == 0 {
		b.WriteString("no semantic events")
		return b.String()
	}
	for _, event := range m.events {
		line := event.Type
		if event.PaneID != "" {
			line += " pane=" + event.PaneID
		}
		if event.Message != "" {
			line += " " + event.Message
		}
		if pane != nil && event.PaneID == pane.ID {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func renderBlock(title, body string, width int) string {
	if width <= 0 {
		return title + "\n" + body
	}
	lines := []string{title}
	for _, line := range strings.Split(body, "\n") {
		lines = append(lines, truncate(line, width))
	}
	return strings.Join(lines, "\n")
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
