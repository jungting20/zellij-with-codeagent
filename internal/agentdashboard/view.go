package agentdashboard

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	connection := strings.ToUpper(m.connection)
	if connection == "" {
		connection = "CONNECTING"
	}
	fmt.Fprintf(&b, "AGENT DASHBOARD  [%s]\n\n", connection)
	b.WriteString("  STATE  AGENT  PROJECT  SINCE\n")
	if !m.loaded {
		b.WriteString("  loading...\n")
	} else if len(m.rows) == 0 {
		b.WriteString("  no managed coding agents\n")
	} else {
		for index, row := range m.rows {
			cursor := "  "
			if index == m.selected {
				cursor = "> "
			}
			state := row.Agent.State
			fmt.Fprintf(&b, "%s%s %-8s %-7s %-24s %s\n",
				cursor, stateSymbol(state), state, agentName(row.Agent.Kind), projectName(row.Pane.CWD), elapsed(m.lastRefresh, row.Agent.StateChangedAt))
		}
	}
	b.WriteString("\n")
	b.WriteString(m.statusText)
	b.WriteString("\n\n")
	b.WriteString("j/k move  Enter focus  R refresh  q quit")
	return b.String()
}

func stateSymbol(state string) string {
	switch state {
	case "working":
		return "●"
	case "blocked":
		return "!"
	case "idle":
		return "○"
	default:
		return "?"
	}
}

func agentName(kind string) string {
	if kind == "" {
		return "Unknown"
	}
	runes := []rune(kind)
	return strings.ToUpper(string(runes[0])) + string(runes[1:])
}

func projectName(cwd string) string {
	name := filepath.Base(filepath.Clean(cwd))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "-"
	}
	return name
}

func elapsed(now, changed time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	if changed.IsZero() || now.Before(changed) {
		return "00:00"
	}
	duration := now.Sub(changed).Round(time.Second)
	minutes := int(duration / time.Minute)
	seconds := int(duration/time.Second) % 60
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}
