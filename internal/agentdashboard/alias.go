package agentdashboard

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/transport"
)

type aliasResultMsg struct {
	agentID, alias string
	err            error
}

func (m Model) openAliasPicker() (tea.Model, tea.Cmd) {
	if m.stopping || m.pinning || m.focusing || len(m.panelIndices(m.focusPinned)) == 0 {
		return m, nil
	}
	row := m.rows[m.selected]
	m.aliasTarget, m.aliasProject = row.Agent.ID, projectName(row.Pane.CWD)
	m.aliasSelected, m.aliasError = 0, ""
	for index, alias := range codingagent.TaskAliases() {
		if string(alias) == row.Agent.TaskAlias {
			m.aliasSelected = index
			break
		}
	}
	return m, nil
}

func (m Model) updateAliasKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.closeStream()
		m.quitting = true
		return m, tea.Quit
	}
	if m.aliasSaving {
		return m, nil
	}
	aliases := codingagent.TaskAliases()
	switch msg.String() {
	case "esc", "q":
		m.aliasTarget, m.aliasError = "", ""
	case "up", "k":
		m.aliasSelected = maxInt(0, m.aliasSelected-1)
	case "down", "j":
		m.aliasSelected = minInt(len(aliases)-1, m.aliasSelected+1)
	case "enter":
		m.aliasSaving, m.aliasError = true, ""
		agentID, alias := m.aliasTarget, string(aliases[m.aliasSelected])
		return m, func() tea.Msg {
			response, err := m.client.SetAgentTaskAlias(m.ctx, agentID, transport.SetAgentTaskAliasRequest{TaskAlias: alias})
			return aliasResultMsg{agentID: agentID, alias: response.Agent.TaskAlias, err: err}
		}
	}
	return m, nil
}

func (m Model) aliasView() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 14
	}
	lines := []string{titleStyle.Render("작업 별명 선택 · " + m.aliasProject)}
	aliases := codingagent.TaskAliases()
	visible := minInt(len(aliases), maxInt(1, height-3))
	start := viewportStart(m.aliasSelected, len(aliases), visible)
	for index := start; index < len(aliases) && index < start+visible; index++ {
		label := "  " + aliases[index].Label()
		if index == m.aliasSelected {
			label = selectedStyle.Render("> " + aliases[index].Label())
		}
		lines = append(lines, label)
	}
	status := fmt.Sprintf("%d/%d", m.aliasSelected+1, len(aliases))
	if m.aliasSaving {
		status = "저장 중…"
	} else if m.aliasError != "" {
		status = m.aliasError
	}
	lines = append(lines, status, "↑/↓ j/k 선택 · Enter 적용 · Esc 취소")
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "…")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}
