package agentdashboard

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/cmd/agent-role/agentworktree"
	"zellij-with-codeagent/internal/listselector"
	"zellij-with-codeagent/internal/transport"
)

type worktreeCreatedMsg struct {
	path string
	err  error
}
type worktreeSelectionMsg struct{ args []string }
type worktreeStartedMsg struct{ err error }

type worktreeClient interface {
	StartAgent(context.Context, transport.StartAgentRequest) (transport.StartAgentResponse, error)
}

func (m Model) openWorktree() (tea.Model, tea.Cmd) {
	if m.worktreeBusy || m.stopping || m.pinning || m.focusing || m.gitRunning || len(m.panelIndices(m.focusPinned)) == 0 {
		return m, nil
	}
	if _, ok := m.client.(worktreeClient); !ok {
		m.statusText = "worktree failed: agent launch unavailable"
		return m, nil
	}
	m.worktreeParent = m.rows[m.selected]
	if m.worktreeParent.Pane.ID == "" || m.worktreeParent.Pane.CWD == "" {
		m.statusText = "worktree failed: agent has no managed working directory"
		return m, nil
	}
	m.worktreeNaming = true
	m.worktreeError = ""
	m.worktreePath = ""
	m.worktreePrompt = textinput.New()
	m.worktreePrompt.Placeholder = "feat/my-task"
	m.worktreePrompt.Width = 48
	return m, m.worktreePrompt.Focus()
}

func (m Model) updateWorktreeNameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.worktreeBusy {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.worktreeNaming = false
		m.worktreePrompt.Blur()
		m.statusText = "worktree cancelled"
		return m, nil
	case "enter":
		branch := strings.TrimSpace(m.worktreePrompt.Value())
		if branch == "" {
			m.worktreeError = "브랜치명을 입력하세요"
			return m, nil
		}
		m.worktreeBusy = true
		m.worktreeError = ""
		m.statusText = "creating worktree…"
		return m, func() tea.Msg {
			path, err := agentworktree.CreateNamed(m.ctx, m.worktreeParent.Pane.CWD, branch)
			return worktreeCreatedMsg{path: path, err: err}
		}
	}
	var cmd tea.Cmd
	m.worktreePrompt, cmd = m.worktreePrompt.Update(msg)
	return m, cmd
}

func (m Model) updateWorktreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.worktreeBusy {
		return m, nil
	}
	if msg.String() == "esc" || msg.String() == "ctrl+c" {
		m.worktreePicker = nil
		m.statusText = "worktree retained: " + m.worktreePath
		return m, nil
	}
	// Capture selection synchronously to guard repeated Enter before the command runs.
	updated, cmd := m.worktreePicker.Update(msg)
	picker := updated.(listselector.Model)
	m.worktreePicker = &picker
	if msg.String() == "enter" || msg.String() == "ctrl+r" {
		m.worktreeBusy = true
	}
	return m, cmd
}

func (m Model) worktreeView() string {
	width := maxInt(1, minInt(60, m.width-4))
	if m.width <= 0 {
		width = 60
	}
	var content string
	if m.worktreeNaming {
		prompt := m.worktreePrompt
		prompt.Width = maxInt(1, width-8)
		content = "Worktree 브랜치명\n" + prompt.View() + "\n브랜치명-HHMMSS로 생성\nEnter 다음 · Esc 취소"
		if m.worktreeError != "" {
			content += "\n" + m.worktreeError
		}
		if m.worktreeBusy {
			content += "\nCreating worktree…"
		}
	} else {
		content = m.worktreePicker.View() + "\n" + m.worktreePath + "\n" + m.statusText
	}
	if m.worktreeBusy && !m.worktreeNaming {
		content += "\nStarting agent…"
	}
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], maxInt(1, width-4), "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).
		Width(maxInt(1, width-2)).Render(strings.Join(lines, "\n"))
}

func (m Model) handleWorktreeCreated(msg worktreeCreatedMsg) (tea.Model, tea.Cmd) {
	m.worktreeBusy = false
	if msg.err != nil {
		m.worktreeError = msg.err.Error()
		m.statusText = "worktree failed: " + msg.err.Error()
		return m, nil
	}
	m.worktreeNaming = false
	m.worktreePrompt.Blur()
	m.worktreePath = msg.path
	picker := listselector.NewPicker(func(_ string, args []string) tea.Cmd {
		return func() tea.Msg { return worktreeSelectionMsg{args: args} }
	})
	m.worktreePicker = &picker
	m.statusText = "worktree: " + msg.path
	return m, picker.Init()
}

func (m Model) startWorktree(msg worktreeSelectionMsg) (tea.Model, tea.Cmd) {
	if m.worktreePicker == nil {
		return m, nil
	}
	m.worktreeBusy = true
	client, ok := m.client.(worktreeClient)
	return m, func() tea.Msg {
		if !ok {
			return worktreeStartedMsg{err: fmt.Errorf("agent launch unavailable")}
		}
		err := agentworktree.Start(m.ctx, client, m.worktreeParent, m.worktreePath, msg.args)
		return worktreeStartedMsg{err: err}
	}
}
