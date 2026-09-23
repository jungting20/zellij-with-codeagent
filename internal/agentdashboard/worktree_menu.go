package agentdashboard

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"zellij-with-codeagent/cmd/agent-role/agentworktreesend"
)

type worktreeMenu struct {
	mode, directory string
	parentID        string
	summary         string
	prompt          textinput.Model
	busy            bool
	output          viewport.Model
}
type worktreeShellResultMsg struct {
	results []agentworktreesend.Result
	err     error
}

func (m Model) openWorktreeMenu() (tea.Model, tea.Cmd) {
	if m.stopping || m.pinning || m.focusing || m.gitRunning || m.worktreeBusy || len(m.panelIndices(m.focusPinned)) == 0 {
		return m, nil
	}
	m.worktrees = &worktreeMenu{directory: m.rows[m.selected].Pane.CWD, parentID: m.rows[m.selected].Agent.ID}
	return m, nil
}
func (m Model) updateWorktreeMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.worktrees
	if p.busy {
		return m, nil
	}
	if msg.String() == "esc" || msg.String() == "ctrl+c" {
		m.worktrees = nil
		return m, nil
	}
	switch p.mode {
	case "":
		switch msg.String() {
		case "g":
			return m.openLazygit()
		case "t":
			return m.focusChildWorktree()
		case "a":
			m.worktrees = nil
			return m.openWorktree()
		case "m":
			m.worktrees = nil
			return m.openMerge()
		case "s":
			p.mode = "send"
			p.prompt = textinput.New()
			p.prompt.CharLimit = 0
			p.prompt.Placeholder = "예: git status --short"
			return m, p.prompt.Focus()
		}
	case "send":
		if msg.String() == "enter" {
			command := p.prompt.Value()
			if strings.TrimSpace(command) == "" {
				return m, nil
			}
			p.busy = true
			parentID := p.parentID
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
				defer cancel()
				results, err := agentworktreesend.ExecuteChildren(ctx, m.client, parentID, command)
				return worktreeShellResultMsg{results: results, err: err}
			}
		}
		var cmd tea.Cmd
		p.prompt, cmd = p.prompt.Update(msg)
		return m, cmd
	case "result":
		var cmd tea.Cmd
		p.output, cmd = p.output.Update(msg)
		return m, cmd
	}
	return m, nil
}
func (m Model) handleWorktreeShellResult(msg worktreeShellResultMsg) (tea.Model, tea.Cmd) {
	if m.worktrees == nil {
		return m, nil
	}
	p := m.worktrees
	p.busy = false
	p.mode = "result"
	var lines []string
	success := 0
	if msg.err != nil {
		lines = append(lines, msg.err.Error())
	}
	for _, r := range msg.results {
		status := "완료"
		if r.Error != "" {
			status = "실패: " + r.Error
		} else {
			success++
		}
		lines = append(lines, r.Path+" · "+status, ansi.Strip(r.Output), "")
	}
	m.statusText = fmt.Sprintf("worktree 명령 실행: %d 성공 / %d 실패", success, len(msg.results)-success)
	if msg.err != nil {
		m.statusText = "worktree 실행 실패: " + msg.err.Error()
	}
	p.summary = m.statusText
	p.output = viewport.New(60, 10)
	p.output.SetContent(strings.Join(lines, "\n"))
	return m, nil
}
func (m Model) worktreeMenuView() string {
	p := m.worktrees
	width := m.width
	if width <= 0 {
		width = 80
	}
	width = maxInt(1, minInt(76, width-4))
	lines := []string{"Worktree", p.directory}
	switch p.mode {
	case "":
		lines = append(lines, "a: 추가", "s: 자식 워크트리에 셸 명령어 실행", "m: merge", "t: 자식 워크트리 탭으로 이동", "g: lazygit", "Esc 닫기")
		if p.summary != "" {
			lines = append(lines, p.summary)
		}
		if p.busy {
			lines = append(lines, "탭 이동 중…")
		}
	case "send":
		prompt := p.prompt
		prompt.Width = maxInt(1, width-6)
		lines = append(lines, "선택한 agent의 직접 자식 워크트리에서 실행", prompt.View(), "Enter 실행 · Esc 취소")
		if p.busy {
			lines = append(lines, "실행 중… (최대 5분)")
		}
	case "result":
		p.output.Width = maxInt(1, width-4)
		p.output.Height = 10
		if m.height > 0 {
			p.output.Height = maxInt(1, m.height-9)
		}
		lines = append(lines, p.summary, p.output.View(), "↑↓ 스크롤 · Esc 닫기")
	}
	lines = strings.Split(strings.Join(lines, "\n"), "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], maxInt(1, width-4), "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(maxInt(1, width-2)).Render(strings.Join(lines, "\n"))
}

// Use the existing focus transport so session/tab navigation stays runtime-owned.
func (m Model) focusChildWorktree() (tea.Model, tea.Cmd) {
	var parentPaneID string
	for _, row := range m.rows {
		if row.Agent.ID == m.worktrees.parentID {
			parentPaneID = row.Pane.ID
			break
		}
	}
	if parentPaneID != "" {
		for _, row := range m.rows {
			if row.Pane.ParentPaneID != parentPaneID || row.Agent.ID == "" || sessionName(row) != "worktree-agent" {
				continue
			}
			if row.Pane.Status != "running" && row.Pane.Status != "starting" {
				continue
			}
			m.focusing = true
			m.worktrees.busy = true
			m.worktrees.summary = ""
			return m, m.focusCmd(row.Agent.ID)
		}
	}
	m.worktrees.summary = "이동할 자식 worktree agent가 없습니다"
	return m, nil
}
