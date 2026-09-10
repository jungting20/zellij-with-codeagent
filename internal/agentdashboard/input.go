package agentdashboard

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/transport"
)

type inputResultMsg struct {
	agentID string
	err     error
}

func (m Model) openInput() (tea.Model, tea.Cmd) {
	if m.stopping || m.pinning || m.focusing || len(m.panelIndices(m.focusPinned)) == 0 {
		return m, nil
	}
	row := m.rows[m.selected]
	if row.Pane.Status != "starting" && row.Pane.Status != "running" {
		m.statusText = "input disabled for inactive pane"
		return m, nil
	}
	paneID := row.Agent.PaneID
	if paneID == "" {
		paneID = row.Pane.ID
	}
	if paneID == "" {
		m.statusText = "input failed: agent has no managed pane"
		return m, nil
	}
	m.inputPane, m.inputAgent, m.inputError = paneID, row.Agent.ID, ""
	m.prompt = textinput.New()
	m.prompt.Placeholder = "프롬프트 입력"
	width := m.width
	if width <= 0 {
		width = 80
	}
	m.prompt.Width = maxInt(1, m.inputPopupWidth()-6)
	m.inputX, m.inputY = m.inputAnchor(width)
	return m, m.prompt.Focus()
}

func (m Model) updateInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.closeStream()
		m.quitting = true
		return m, tea.Quit
	}
	if m.inputSending {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.inputPane, m.inputAgent, m.inputError = "", "", ""
		m.prompt.Reset()
		return m, nil
	case "enter":
		text := m.prompt.Value()
		if strings.TrimSpace(text) == "" {
			return m, nil
		}
		m.inputSending, m.inputError = true, ""
		paneID, agentID := m.inputPane, m.inputAgent
		return m, func() tea.Msg {
			err := m.client.SendInput(m.ctx, paneID, transport.SendInputRequest{Text: text + "\n"})
			return inputResultMsg{agentID: agentID, err: err}
		}
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

// inputAnchor records the selected row's screen position when input opens.
func (m Model) inputAnchor(width int) (int, int) {
	x, panelWidth := 0, width
	if width >= 100 {
		leftWidth := (width - 3) * 35 / 100
		panelWidth = leftWidth
		if !m.focusPinned {
			x, panelWidth = leftWidth+3, width-leftWidth-3
		}
	}
	bodyHeight := maxInt(3, len(m.displayRows())+2)
	if m.height > 0 {
		bodyHeight = maxInt(1, m.height-3-len(m.activityView()))
	}
	for index, line := range m.panelView(m.focusPinned, panelWidth, bodyHeight) {
		if strings.HasPrefix(ansi.Strip(line), ">") {
			return x + 2, index + 1
		}
	}
	return x + 2, 2
}

func (m Model) inputPopupWidth() int {
	width := m.width
	if width <= 0 {
		width = 80
	}
	return maxInt(1, minInt(60, width-4))
}

func (m Model) inputView() string {
	width := m.inputPopupWidth()
	contentWidth := maxInt(1, width-4)
	m.prompt.Width = maxInt(1, width-6)
	status := m.inputError
	if m.inputSending {
		status = "전송 중…"
	}
	lines := []string{titleStyle.Render("프롬프트 · " + m.inputAgent), m.prompt.View(), status, "Enter 전송 · Esc 취소"}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], contentWidth, "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("42")).
		Padding(0, 1).Width(maxInt(1, width-2)).Render(strings.Join(lines, "\n"))
}

func (m Model) inputOverlay(base string) string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	lines := strings.Split(base, "\n")
	if height <= 0 {
		height = maxInt(8, len(lines))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	popup := strings.Split(m.inputView(), "\n")
	popupWidth := 0
	for _, line := range popup {
		popupWidth = maxInt(popupWidth, ansi.StringWidth(line))
	}
	x := maxInt(0, minInt(m.inputX, width-popupWidth))
	y := m.inputY + 1
	// Prefer below the selected row, then above it, then clamp to the screen.
	if y+len(popup) > height {
		y = m.inputY - len(popup)
	}
	y = maxInt(0, minInt(y, height-len(popup)))
	for index, line := range popup {
		if y+index >= height {
			break
		}
		baseLine := padCell(lines[y+index], width)
		lines[y+index] = ansi.Cut(baseLine, 0, x) +
			ansi.Truncate(padCell(line, popupWidth), width-x, "") +
			ansi.Cut(baseLine, minInt(width, x+popupWidth), width)
	}
	return strings.Join(lines[:height], "\n")
}
