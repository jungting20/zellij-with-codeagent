package dashboard

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/transport"
)

type actionResultMsg struct {
	kind    string
	summary string
	err     error
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case "input":
		return m.updateInputKey(msg)
	case "confirm-cleanup":
		return m.updateCleanupKey(msg)
	default:
		return m.updateActionOrNormalKey(msg)
	}
}

func (m Model) updateActionOrNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "i":
		pane := m.selectedPane()
		if pane == nil {
			m.statusText = "input requires a selected pane"
			return m, nil
		}
		if pane.Status != "starting" && pane.Status != "running" {
			m.statusText = "input disabled for inactive pane status=" + valueOrDash(pane.Status)
			return m, nil
		}
		if m.actionInFlight {
			return m, nil
		}
		m.mode = "input"
		m.input = nil
		m.inputPane = pane.ID
		m.statusText = "input for " + pane.ID
		return m, nil
	case "r":
		if m.actionInFlight {
			return m, nil
		}
		m.actionInFlight = true
		m.statusText = "reconciling runtime"
		return m, m.reconcileCmd()
	case "x":
		pane := m.selectedPane()
		if pane == nil {
			m.statusText = "cleanup requires a selected pane"
			return m, nil
		}
		if strings.TrimSpace(pane.TaskID) == "" {
			m.statusText = "cleanup requires a pane with a task id"
			return m, nil
		}
		if m.actionInFlight {
			return m, nil
		}
		m.mode = "confirm-cleanup"
		m.confirmTask = pane.TaskID
		m.statusText = fmt.Sprintf("cleanup task %s (%d panes)? y/N", pane.TaskID, m.taskPaneCount(pane.TaskID))
		return m, nil
	}
	return m.updateNormalKey(msg)
}

func (m Model) updateInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = "normal"
		m.input = nil
		m.inputPane = ""
		m.statusText = "input cancelled"
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case tea.KeyEnter:
		if len(m.input) == 0 || m.actionInFlight {
			return m, nil
		}
		paneID := m.inputPane
		if paneID == "" {
			m.mode = "normal"
			m.input = nil
			m.statusText = "input failed: target pane is unavailable"
			return m, nil
		}
		text := string(m.input)
		m.actionInFlight = true
		m.statusText = "sending input to " + paneID
		return m, m.sendInputCmd(paneID, text)
	case tea.KeyRunes:
		m.input = append(m.input, msg.Runes...)
		return m, nil
	}
	return m, nil
}

func (m Model) updateCleanupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if m.actionInFlight || m.confirmTask == "" {
			return m, nil
		}
		taskID := m.confirmTask
		m.actionInFlight = true
		m.statusText = "cleaning task " + taskID
		return m, m.cleanupCmd(taskID)
	case "n", "N", "esc":
		m.mode = "normal"
		m.confirmTask = ""
		m.statusText = "cleanup cancelled"
		return m, nil
	}
	return m, nil
}

func (m Model) sendInputCmd(paneID, text string) tea.Cmd {
	return func() tea.Msg {
		err := m.client.SendInput(m.ctx, paneID, transport.SendInputRequest{Text: text + "\n"})
		return actionResultMsg{kind: "input", summary: "input sent to " + paneID, err: err}
	}
}

func (m Model) reconcileCmd() tea.Cmd {
	return func() tea.Msg {
		response, err := m.client.Reconcile(m.ctx)
		return actionResultMsg{
			kind:    "reconcile",
			summary: fmt.Sprintf("reconciled active=%d lost=%d", len(response.Active), len(response.Lost)),
			err:     err,
		}
	}
}

func (m Model) cleanupCmd(taskID string) tea.Cmd {
	return func() tea.Msg {
		response, err := m.client.Cleanup(m.ctx, transport.CleanupRequest{TaskID: taskID})
		return actionResultMsg{
			kind:    "cleanup",
			summary: fmt.Sprintf("cleanup closed=%d failed=%d skipped=%d", len(response.Closed), len(response.Failed), len(response.Skipped)),
			err:     err,
		}
	}
}

func (m Model) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	m.actionInFlight = false
	m.mode = "normal"
	m.input = nil
	m.inputPane = ""
	m.confirmTask = ""
	if msg.err != nil {
		m.statusText = msg.kind + " failed: " + msg.err.Error()
		m.actionText = m.statusText
		return m, nil
	}
	m.statusText = msg.summary
	m.actionText = msg.summary
	return m, m.requestRefresh()
}

func (m Model) taskPaneCount(taskID string) int {
	count := 0
	for _, row := range m.rows {
		if row.node.pane != nil && row.node.pane.TaskID == taskID {
			count++
		}
	}
	return count
}
