package agentdashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	m.aliasSelected, m.aliasError = len(codingagent.TaskAliases()), ""
	m.aliasCustom = false
	m.aliasPrompt = textinput.New()
	m.aliasPrompt.Placeholder = "태그 직접입력"
	m.aliasPrompt.SetValue(row.Agent.TaskAlias)
	width := m.width
	if width <= 0 {
		width = 80
	}
	m.aliasX, m.aliasY = m.inputAnchor(width)
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
	if m.aliasCustom {
		switch msg.String() {
		case "esc":
			m.aliasCustom, m.aliasError = false, ""
			m.aliasPrompt.Blur()
			return m, nil
		case "enter":
			alias := strings.TrimSpace(m.aliasPrompt.Value())
			if alias == "" {
				return m, nil
			}
			if !codingagent.TaskAlias(alias).Valid() {
				m.aliasError = "한 줄 태그를 입력해주세요"
				return m, nil
			}
			return m.saveAlias(alias)
		}
		var cmd tea.Cmd
		m.aliasPrompt, cmd = m.aliasPrompt.Update(msg)
		return m, cmd
	}
	aliases := codingagent.TaskAliases()
	switch msg.String() {
	case "esc", "q":
		m.aliasTarget, m.aliasError = "", ""
	case "up", "k":
		m.aliasSelected = maxInt(0, m.aliasSelected-1)
	case "down", "j":
		m.aliasSelected = minInt(len(aliases), m.aliasSelected+1)
	case "enter":
		if m.aliasSelected == len(aliases) {
			m.aliasCustom, m.aliasError = true, ""
			return m, m.aliasPrompt.Focus()
		}
		return m.saveAlias(string(aliases[m.aliasSelected]))
	}
	return m, nil
}

func (m Model) saveAlias(alias string) (tea.Model, tea.Cmd) {
	m.aliasSaving, m.aliasError = true, ""
	agentID := m.aliasTarget
	return m, func() tea.Msg {
		response, err := m.client.SetAgentTaskAlias(m.ctx, agentID, transport.SetAgentTaskAliasRequest{TaskAlias: alias})
		return aliasResultMsg{agentID: agentID, alias: response.Agent.TaskAlias, err: err}
	}
}

func (m Model) aliasView() string {
	width := minInt(44, m.inputPopupWidth())
	contentWidth := maxInt(1, width-4)
	lines := []string{titleStyle.Render("태그 · " + m.aliasProject)}
	labels := []string{}
	for _, alias := range codingagent.TaskAliases() {
		labels = append(labels, alias.Label())
	}
	labels = append(labels, "직접입력")
	status := fmt.Sprintf("%d/%d", m.aliasSelected+1, len(labels))
	help := "↑/↓ 선택 · Enter 적용 · Esc 취소"
	if m.aliasCustom {
		m.aliasPrompt.Width = maxInt(1, contentWidth-2)
		lines = append(lines, "직접입력", m.aliasPrompt.View())
		status = ""
		help = "Enter 저장 · Esc 목록"
	} else {
		visible := len(labels)
		if m.height > 0 {
			visible = minInt(visible, maxInt(1, m.height-5))
		}
		start := viewportStart(m.aliasSelected, len(labels), visible)
		for index := start; index < len(labels) && index < start+visible; index++ {
			label := "  " + labels[index]
			if index == m.aliasSelected {
				label = selectedStyle.Render("> " + labels[index])
			}
			lines = append(lines, label)
		}
	}
	if m.aliasSaving {
		status = "저장 중…"
	} else if m.aliasError != "" {
		status = m.aliasError
	}
	lines = append(lines, status, help)
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], contentWidth, "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("42")).
		Padding(0, 1).Width(maxInt(1, width-2)).Render(strings.Join(lines, "\n"))
}
