package agentdashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"zellij-with-codeagent/cmd/agent-role/agentticket"
	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

// ticketPopup is transient UI state; its target stays fixed across agent refreshes.
type ticketPopup struct {
	agentIndex   int
	defaultAgent string
	agentReady   bool
	target       transport.AgentWithPane
	mode         string
	busy         bool
	err          string
	prompt       textarea.Model
	list         viewport.Model
	tickets      []ticketworker.Ticket
}
type ticketResultMsg struct {
	action, output string
	err            error
}

func (m Model) openTickets() (tea.Model, tea.Cmd) {
	if m.stopping || m.pinning || m.focusing || m.gitRunning || len(m.panelIndices(m.focusPinned)) == 0 {
		return m, nil
	}
	row := m.rows[m.selected]
	if strings.TrimSpace(row.Pane.CWD) == "" {
		m.statusText = "ticket failed: agent has no working directory"
		return m, nil
	}
	m.ticket = &ticketPopup{target: row, mode: "menu", list: viewport.New(50, 8)}
	m.resizeTickets()
	return m, nil
}

func (m Model) ticketPopupWidth() int {
	if m.ticket == nil || m.ticket.mode != "list" {
		return m.inputPopupWidth()
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	return maxInt(1, minInt(width-4, width*9/10))
}

func (m *Model) resizeTickets() {
	if m.ticket == nil {
		return
	}
	width := maxInt(1, m.ticketPopupWidth()-4)
	height := 8
	if m.height > 0 {
		height = maxInt(1, m.height-8)
	}
	m.ticket.list.Width, m.ticket.list.Height = width, height
	if m.ticket.mode == "add" {
		m.ticket.prompt.SetWidth(width)
	}
	var lines []string
	for _, ticket := range m.ticket.tickets {
		lines = append(lines, fmt.Sprintf("#%d [%s] %s · 등록: %s", ticket.ID, ticket.Status, ticket.Title, ticket.Agent), ticket.Prompt, "")
	}
	if len(lines) == 0 {
		lines = []string{"등록된 티켓이 없습니다"}
	}
	m.ticket.list.SetContent(ansi.Hardwrap(ansi.Strip(strings.Join(lines, "\n")), width, true))
}

func (m Model) updateTicketKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.closeStream()
		m.quitting = true
		return m, tea.Quit
	}
	if m.ticket.busy {
		return m, nil
	}
	if msg.String() == "esc" {
		m.ticket = nil
		return m, nil
	}
	switch m.ticket.mode {
	case "menu":
		switch msg.String() {
		case "s":
			return m.openTicketAgentPicker()
		case "l":
			m.ticket.mode = "list"
			m.resizeTickets()
			return m.runTicket("list")
		case "a":
			m.ticket.mode, m.ticket.err = "add", ""
			m.ticket.prompt = textarea.New()
			m.ticket.prompt.CharLimit = 0
			m.ticket.prompt.ShowLineNumbers = false
			m.ticket.prompt.Placeholder = "티켓 프롬프트 입력"
			m.ticket.prompt.SetHeight(3)
			m.resizeTickets()
			return m, m.ticket.prompt.Focus()
		}
	case "start":
		switch msg.String() {
		case "up", "k":
			m.ticket.agentIndex = (m.ticket.agentIndex + len(ticketAgentKinds) - 1) % len(ticketAgentKinds)
		case "down", "j":
			m.ticket.agentIndex = (m.ticket.agentIndex + 1) % len(ticketAgentKinds)
		case "r":
			return m.openTicketAgentPicker()
		case "enter":
			if m.ticket.agentReady {
				return m.runTicket("start")
			}
		}
	case "add":
		if msg.String() == "enter" {
			if strings.TrimSpace(m.ticket.prompt.Value()) == "" {
				m.ticket.err = "프롬프트를 입력하세요"
				return m, nil
			}
			return m.runTicket("add")
		}
		var cmd tea.Cmd
		m.ticket.prompt, cmd = m.ticket.prompt.Update(msg)
		return m, cmd
	case "list":
		if msg.String() == "r" {
			return m.runTicket("list")
		}
		var cmd tea.Cmd
		m.ticket.list, cmd = m.ticket.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) runTicket(action string) (tea.Model, tea.Cmd) {
	m.ticket.busy, m.ticket.err = true, ""
	target := m.ticket.target
	req := agentticket.Request{Action: action, Directory: target.Pane.CWD, Agent: target.Agent.Kind, Prompt: m.ticket.prompt.Value(), Session: target.Pane.SessionID, Socket: m.opts.SocketPath, Timeout: m.opts.RequestTimeout}
	if action == "start" {
		req.DefaultAgent = string(ticketAgentKinds[m.ticket.agentIndex])
	}
	return m, func() tea.Msg {
		output, err := agentticket.Execute(m.ctx, req)
		return ticketResultMsg{action: action, output: output, err: err}
	}
}

func (m Model) handleTicketResult(msg ticketResultMsg) (tea.Model, tea.Cmd) {
	if m.ticket == nil {
		return m, nil
	}
	m.ticket.busy = false
	if msg.err != nil {
		m.ticket.err = msg.err.Error()
		return m, nil
	}
	switch msg.action {
	case "list":
		if err := json.Unmarshal([]byte(msg.output), &m.ticket.tickets); err != nil {
			m.ticket.err = "ticket list failed: " + err.Error()
			return m, nil
		}
		m.resizeTickets()
		m.ticket.list.GotoTop()
	case "add":
		var ticket ticketworker.Ticket
		if err := json.Unmarshal([]byte(msg.output), &ticket); err != nil {
			// A successful mutation must not be retried merely because its output was malformed.
			m.statusText = "티켓 등록 완료 (결과 해석 실패)"
		} else {
			m.statusText = fmt.Sprintf("티켓 #%d 등록 완료", ticket.ID)
		}
		m.ticket = nil
	case "start":
		m.statusText = "티켓 매니저 시작 완료"
		m.ticket = nil
	}
	return m, nil
}

func (m Model) ticketView() string {
	width := m.ticketPopupWidth()
	lines := []string{titleStyle.Render("Ticket · " + m.ticket.target.Agent.ID)}
	switch m.ticket.mode {
	case "menu":
		lines = append(lines, "s: ticket start", "a: ticket add", "l: ticket list", "Esc 닫기")
	case "start":
		lines = append(lines, "Worker 에이전트 선택")
		for index, kind := range ticketAgentKinds {
			label := "  " + string(kind)
			if string(kind) == m.ticket.defaultAgent {
				label += " (default_agent)"
			}
			if index == m.ticket.agentIndex {
				label = "> " + label[2:]
				label = selectedStyle.Render(label)
			}
			lines = append(lines, label)
		}
		lines = append(lines, "↑/↓ 선택 · Enter 시작 · Esc 취소", "이번 매니저의 모든 worker에 적용")
		if !m.ticket.agentReady && !m.ticket.busy {
			lines = append(lines, "r 설정 다시 읽기")
		}
	case "add":
		lines = append(lines, m.ticket.prompt.View(), "Enter 등록 · Alt+Enter 줄바꿈 · Esc 취소")
	case "list":
		lines = append(lines, m.ticket.list.View(), "↑/↓ PgUp/PgDn 스크롤 · r 새로고침 · Esc 닫기")
	}
	if m.ticket.busy {
		lines = append(lines, "처리 중…")
	}
	if m.ticket.err != "" {
		lines = append(lines, errorStyle.Render(ansi.Hardwrap(m.ticket.err, maxInt(1, width-4), true)))
	}
	var output []string
	for _, line := range strings.Split(strings.Join(lines, "\n"), "\n") {
		output = append(output, ansi.Truncate(line, maxInt(1, width-4), "…"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(maxInt(1, width-2)).Render(strings.Join(output, "\n"))
}

var ticketAgentKinds = []codingagent.Kind{codingagent.KindCodex, codingagent.KindClaude, codingagent.KindGemini, codingagent.KindCursor, codingagent.KindHermes}

type ticketAgentConfigMsg struct {
	target *ticketPopup
	agent  string
	err    error
}

func (m Model) openTicketAgentPicker() (tea.Model, tea.Cmd) {
	m.ticket.mode, m.ticket.busy, m.ticket.err = "start", true, ""
	m.ticket.agentReady = false
	popup := m.ticket
	return m, func() tea.Msg {
		agent, err := agentticket.DefaultAgent(popup.target.Pane.CWD)
		return ticketAgentConfigMsg{target: popup, agent: agent, err: err}
	}
}

func (m Model) handleTicketAgentConfig(msg ticketAgentConfigMsg) (tea.Model, tea.Cmd) {
	if m.ticket == nil || m.ticket != msg.target {
		return m, nil
	}
	m.ticket.busy = false
	if msg.err != nil {
		m.ticket.err = msg.err.Error()
		return m, nil
	}
	for index, kind := range ticketAgentKinds {
		if string(kind) == msg.agent {
			m.ticket.agentIndex, m.ticket.defaultAgent, m.ticket.agentReady = index, msg.agent, true
			return m, nil
		}
	}
	m.ticket.err = "지원하지 않는 default_agent: " + msg.agent
	return m, nil
}
