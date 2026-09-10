package agentdashboard

import (
	"fmt"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	lazygitrole "zellij-with-codeagent/cmd/agent-role/lazygit"
)

type lazygitResultMsg struct{ err error }

func (m Model) lazygitCommand() (*exec.Cmd, error) {
	if len(m.panelIndices(m.focusPinned)) == 0 {
		return nil, fmt.Errorf("select an agent first")
	}
	return lazygitrole.Command(m.rows[m.selected].Pane.CWD)
}

func (m Model) openLazygit() (tea.Model, tea.Cmd) {
	if m.stopping || m.pinning || m.focusing || m.gitRunning {
		return m, nil
	}
	cmd, err := m.lazygitCommand()
	if err != nil {
		m.statusText = "lazygit failed: " + err.Error()
		return m, nil
	}
	m.gitRunning = true
	m.statusText = "lazygit: " + cmd.Dir
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return lazygitResultMsg{err: err}
	})
}
