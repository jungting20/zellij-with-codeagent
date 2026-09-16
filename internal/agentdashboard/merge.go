package agentdashboard

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"zellij-with-codeagent/cmd/agent-role/agentworktreemerge"
)

type mergeResultMsg struct{ err error }

func (m Model) openMerge() (tea.Model, tea.Cmd) {
	if m.stopping || m.pinning || m.focusing || m.gitRunning || time.Now().Before(m.mergeCooldown) || len(m.panelIndices(m.focusPinned)) == 0 {
		return m, nil
	}
	parent := m.rows[m.selected]
	if parent.Pane.ID == "" || parent.Pane.Status != "running" || parent.Agent.State != "idle" {
		m.statusText = "merge: 부모 agent가 idle일 때 요청하세요"
		return m, nil
	}
	m.mergeChildren = nil
	for _, row := range m.rows {
		if row.Pane.ParentPaneID == parent.Pane.ID && row.Pane.CWD != "" && row.Pane.CWD != parent.Pane.CWD {
			m.mergeChildren = append(m.mergeChildren, row)
		}
	}
	if len(m.mergeChildren) == 0 {
		m.statusText = "merge: 자식 worktree agent가 없습니다"
		return m, nil
	}
	m.mergeParent = parent.Agent.ID
	m.mergeSelected = 0
	if len(m.mergeChildren) == 1 {
		return m.sendMerge()
	}
	return m, nil
}

func (m Model) sendMerge() (tea.Model, tea.Cmd) {
	child := m.mergeChildren[m.mergeSelected]
	if child.Agent.State != "idle" || child.Pane.Status != "running" {
		m.statusText = "merge: 자식 agent가 idle일 때 요청하세요"
		m.mergeParent = ""
		m.mergeChildren = nil
		return m, nil
	}
	m.mergeBusy = true
	m.statusText = "merge 요청 전송 중…"
	parentID, childID := m.mergeParent, child.Agent.ID
	return m, func() tea.Msg {
		return mergeResultMsg{err: agentworktreemerge.Request(m.ctx, m.client, parentID, childID)}
	}
}

func (m Model) updateMergeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mergeBusy {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mergeParent = ""
		m.mergeChildren = nil
		m.statusText = "merge 요청 취소"
	case "up", "k":
		m.mergeSelected = maxInt(0, m.mergeSelected-1)
	case "down", "j":
		m.mergeSelected = minInt(len(m.mergeChildren)-1, m.mergeSelected+1)
	case "enter":
		return m.sendMerge()
	}
	return m, nil
}

func (m Model) mergeView() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	width = maxInt(1, minInt(70, width-4))
	lines := []string{"병합할 자식 worktree 선택"}
	// Keep the selection visible even with many children or a short terminal.
	count := maxInt(1, m.height-8)
	if m.height <= 0 {
		count = 8
	}
	start := maxInt(0, m.mergeSelected-count+1)
	end := minInt(len(m.mergeChildren), start+count)
	for i := start; i < end; i++ {
		child := m.mergeChildren[i]
		marker := " "
		if i == m.mergeSelected {
			marker = ">"
		}
		lines = append(lines, fmt.Sprintf("%s %s [%s] %s", marker, child.Agent.ID, child.Agent.State, child.Pane.CWD))
	}
	lines = append(lines, "↑↓ 선택 · Enter 요청 · Esc 취소")
	if m.mergeBusy {
		lines = append(lines, "전송 중…")
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], maxInt(1, width-4), "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(maxInt(1, width-2)).Render(strings.Join(lines, "\n"))
}
