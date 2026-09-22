package agentdashboard

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"zellij-with-codeagent/internal/followup"
	"zellij-with-codeagent/internal/transport"
)

type followupClient interface {
	GetAgentFollowups(context.Context, string) (transport.AgentFollowupsResponse, error)
	UpdateAgentFollowups(context.Context, string, transport.UpdateAgentFollowupsRequest) (transport.AgentFollowupsResponse, error)
}

type followupPopup struct {
	agentID, label string
	queue          followup.Queue
	selected       int
	mode           string
	prompt         textarea.Model
	itemID         string
	requestID      string
	busy, loading  bool
	loaded         bool
	sequence       uint64
	err, loadErr   string
}

type followupResultMsg struct {
	target   *followupPopup
	sequence uint64
	action   string
	response transport.AgentFollowupsResponse
	err      error
}

func (m Model) openFollowups() (tea.Model, tea.Cmd) {
	if m.stopping || m.pinning || m.focusing || m.gitRunning || len(m.panelIndices(m.focusPinned)) == 0 {
		return m, nil
	}
	if _, ok := m.client.(followupClient); !ok {
		m.statusText = "후속 지시를 지원하지 않는 연결입니다"
		return m, nil
	}
	row := m.rows[m.selected]
	label := row.Agent.TaskAlias
	if label == "" {
		label = row.Agent.ID
	}
	m.followups = &followupPopup{agentID: row.Agent.ID, label: label, mode: "list"}
	return m, m.requestFollowupRefresh()
}

// Reads and mutations share a generation so an older read cannot undo a saved edit.
func (m *Model) requestFollowupRefresh() tea.Cmd {
	p := m.followups
	if p == nil || p.loading || p.busy {
		return nil
	}
	client, ok := m.client.(followupClient)
	if !ok {
		return nil
	}
	p.loading = true
	p.sequence++
	sequence, agentID := p.sequence, p.agentID
	return func() tea.Msg {
		response, err := client.GetAgentFollowups(m.ctx, agentID)
		return followupResultMsg{target: p, sequence: sequence, action: "get", response: response, err: err}
	}
}

func (m Model) updateFollowupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.followups
	if msg.String() == "ctrl+c" {
		m.closeStream()
		m.quitting = true
		return m, tea.Quit
	}
	if p.busy {
		return m, nil
	}
	if msg.String() == "esc" {
		if p.mode != "list" {
			p.mode, p.itemID, p.requestID, p.err = "list", "", "", ""
			p.prompt.Blur()
			p.prompt.Reset()
		} else {
			m.followups = nil
		}
		return m, nil
	}
	if p.mode != "list" {
		switch msg.String() {
		case "enter":
			if strings.TrimSpace(p.prompt.Value()) == "" {
				p.err = "후속 지시를 입력하세요"
				return m, nil
			}
			return m.runFollowup(transport.UpdateAgentFollowupsRequest{
				Action: p.mode, ItemID: p.itemID, Text: p.prompt.Value(), RequestID: p.requestID,
			})
		case "alt+enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		}
		var cmd tea.Cmd
		p.prompt, cmd = p.prompt.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "up", "k":
		p.selected = maxInt(0, p.selected-1)
	case "down", "j":
		p.selected = maxInt(0, minInt(len(p.queue.Items)-1, p.selected+1))
	case "r":
		return m, m.requestFollowupRefresh()
	case "a":
		return m.editFollowup("add", "", "")
	case "e", "d":
		item := p.selectedItem()
		canCancelAttention := item != nil && msg.String() == "d" && item.State == "needs_attention" && item.ID == p.queue.ActiveItemID
		if item == nil || item.State != "queued" && !canCancelAttention {
			p.err = "대기 중인 지시만 수정하거나 취소할 수 있습니다"
			return m, nil
		}
		if msg.String() == "e" {
			return m.editFollowup("edit", item.ID, item.Text)
		}
		return m.runFollowup(transport.UpdateAgentFollowupsRequest{Action: "cancel", ItemID: item.ID})
	case "p":
		if !p.loaded {
			p.err = "목록을 불러온 뒤 전달 상태를 변경할 수 있습니다"
			return m, nil
		}
		action := "pause"
		if p.queue.Paused {
			action = "resume"
		}
		return m.runFollowup(transport.UpdateAgentFollowupsRequest{Action: action})
	case "c":
		item := p.selectedItem()
		if item == nil || item.State != "needs_attention" {
			p.err = "전송 확인이 필요한 지시를 선택하세요"
			return m, nil
		}
		return m.runFollowup(transport.UpdateAgentFollowupsRequest{Action: "resolve", ItemID: item.ID})
	}
	return m, nil
}

func (p *followupPopup) selectedItem() *followup.Item {
	if p.selected < 0 || p.selected >= len(p.queue.Items) {
		return nil
	}
	return &p.queue.Items[p.selected]
}

func (m Model) editFollowup(mode, id, text string) (tea.Model, tea.Cmd) {
	p := m.followups
	p.mode, p.itemID, p.err = mode, id, ""
	p.requestID = ""
	if mode == "add" {
		p.requestID = uuid.NewString()
	}
	p.prompt = textarea.New()
	p.prompt.CharLimit = 0
	p.prompt.ShowLineNumbers = false
	p.prompt.Prompt = "> "
	p.prompt.Placeholder = "다음에 실행할 지시"
	p.prompt.SetValue(text)
	m.resizeFollowups()
	return m, p.prompt.Focus()
}

func (m Model) runFollowup(req transport.UpdateAgentFollowupsRequest) (tea.Model, tea.Cmd) {
	p := m.followups
	client := m.client.(followupClient)
	p.busy, p.loading, p.err = true, false, ""
	p.sequence++
	sequence, agentID := p.sequence, p.agentID
	return m, func() tea.Msg {
		response, err := client.UpdateAgentFollowups(m.ctx, agentID, req)
		return followupResultMsg{target: p, sequence: sequence, action: req.Action, response: response, err: err}
	}
}

func (m Model) handleFollowupResult(msg followupResultMsg) (tea.Model, tea.Cmd) {
	p := m.followups
	if p == nil || p != msg.target || p.sequence != msg.sequence {
		return m, nil
	}
	if msg.action == "get" {
		p.loading = false
		if msg.err != nil {
			p.loadErr = "목록 조회 실패: " + msg.err.Error()
			return m, nil
		}
	} else {
		p.busy = false
		if msg.err != nil {
			p.err = "변경 실패: " + msg.err.Error()
			return m, nil
		}
		if msg.action == "add" || msg.action == "edit" {
			p.mode, p.itemID, p.requestID = "list", "", ""
			p.prompt.Blur()
			p.prompt.Reset()
		}
		p.err = ""
		if msg.action == "resolve" {
			p.err = "전달 확인 완료 · 일시정지 유지 · p로 재개"
		}
	}
	selectedID := ""
	if item := p.selectedItem(); item != nil {
		selectedID = item.ID
	}
	p.queue, p.loaded, p.loadErr = msg.response.Queue, true, ""
	for index, item := range p.queue.Items {
		if item.ID == selectedID {
			p.selected = index
			break
		}
	}
	p.selected = maxInt(0, minInt(p.selected, len(p.queue.Items)-1))
	for i := range m.rows {
		if m.rows[i].Agent.ID != p.agentID {
			continue
		}
		agent := &m.rows[i].Agent
		agent.FollowupCount, agent.FollowupAttention = p.queue.PendingCount(), p.queue.Phase == followup.NeedsAttention
		agent.FollowupPaused = p.queue.Paused
		for _, item := range p.queue.Items {
			if item.State == "needs_attention" {
				agent.FollowupAttention = true
			}
		}
	}
	return m, nil
}

func (m Model) followupPopupWidth() int {
	width := m.width
	if width <= 0 {
		width = 80
	}
	return maxInt(1, minInt(86, width-2))
}

func (m *Model) resizeFollowups() {
	if m.followups == nil || m.followups.mode == "list" {
		return
	}
	m.followups.prompt.SetWidth(maxInt(1, m.followupPopupWidth()-4))
	height := 5
	if m.height > 0 {
		height = maxInt(1, minInt(height, m.height-8))
	}
	m.followups.prompt.SetHeight(height)
}

func (m Model) followupView() string {
	p := m.followups
	width := maxInt(1, m.followupPopupWidth()-4)
	lines := []string{titleStyle.Render("후속 지시 · " + p.label)}
	status := "자동 전달"
	if p.queue.Paused {
		status = "일시정지"
	}
	if p.loading {
		status += " · 불러오는 중…"
	}
	if p.busy {
		status += " · 저장 중…"
	}
	lines = append(lines, status)
	if p.queue.Reason != "" {
		lines = append(lines, p.queue.Reason)
	}
	if p.err != "" {
		lines = append(lines, errorStyle.Render(p.err))
	}
	if p.loadErr != "" {
		lines = append(lines, errorStyle.Render(p.loadErr))
	}
	if p.mode != "list" {
		label := "지시 추가"
		if p.mode == "edit" {
			label = "지시 수정"
		}
		lines = append(lines, label)
		lines = append(lines, strings.Split(p.prompt.View(), "\n")...)
		lines = append(lines, "Enter 저장 · Alt+Enter 줄바꿈 · Esc 취소")
	} else {
		help := []string{"a 추가 · e 수정 · d 취소 · p 일시정지/재개", "↑/↓ 선택 · r 새로고침 · c 전달 확인 · Esc 닫기"}
		visible := 8
		if m.height > 0 {
			visible = maxInt(1, m.height-2-len(lines)-len(help)-2)
		}
		start := viewportStart(p.selected, len(p.queue.Items), visible)
		if len(p.queue.Items) == 0 {
			lines = append(lines, "예약된 후속 지시가 없습니다")
		}
		for i := start; i < len(p.queue.Items) && i < start+visible; i++ {
			item := p.queue.Items[i]
			prefix := "  "
			if i == p.selected {
				prefix = "> "
			}
			lines = append(lines, fmt.Sprintf("%s%d. [%s] %s", prefix, i+1, followupStateLabel(string(item.State)), strings.Join(strings.Fields(ansi.Strip(item.Text)), " ")))
		}
		if item := p.selectedItem(); item != nil && item.Error != "" {
			lines = append(lines, errorStyle.Render("전송 오류: "+item.Error))
		}
		if item := p.selectedItem(); item != nil && item.State == "needs_attention" {
			lines = append(lines, "c 전달 확인 · d 제외 (자동 재전송 없음)")
		}
		lines = append(lines, help...)
	}
	for i := range lines {
		lines[i] = ansi.Truncate(strings.ReplaceAll(strings.ReplaceAll(lines[i], "\n", " "), "\r", ""), width, "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).
		Width(maxInt(1, m.followupPopupWidth()-2)).Render(strings.Join(lines, "\n"))
}

func followupStateLabel(state string) string {
	switch state {
	case "queued":
		return "대기"
	case "sending":
		return "전송 중"
	case "sent":
		return "전송됨"
	case "needs_attention":
		return "전송 확인 필요"
	case "canceled":
		return "취소됨"
	default:
		return state
	}
}

func followupBadge(agent transport.Agent) string {
	if agent.FollowupCount == 0 && !agent.FollowupPaused && !agent.FollowupAttention {
		return ""
	}
	badge := fmt.Sprintf("후속 %d", agent.FollowupCount)
	if agent.FollowupAttention {
		badge += " 확인!"
	} else if agent.FollowupPaused {
		badge += " 정지"
	}
	return "[" + badge + "]"
}
