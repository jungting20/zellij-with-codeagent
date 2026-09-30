package agentdashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Keep stored input verbatim; sanitize only its terminal presentation.
func instructionText(text string) string {
	text = strings.ReplaceAll(ansi.Strip(text), "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 32 && r != '\n' || r == 127 {
			return -1
		}
		return r
	}, text)
}

func instructionAge(now, at time.Time) string {
	if at.IsZero() {
		return "시간 정보 없음"
	}
	age := max(time.Duration(0), now.Sub(at))
	switch {
	case age < time.Minute:
		return "방금 전"
	case age < time.Hour:
		return fmt.Sprintf("%d분 전", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%d시간 전", int(age/time.Hour))
	default:
		return fmt.Sprintf("%d일 전", int(age/(24*time.Hour)))
	}
}

func (m Model) instructionView(available, width int) []string {
	if available < 2 || !m.loaded || m.selected < 0 || m.selected >= len(m.rows) || len(m.panelIndices(m.focusPinned)) == 0 {
		return nil
	}
	pane := m.rows[m.selected].Pane
	if pane.LastInstruction == "" {
		return nil
	}
	now := m.activityNow
	if now.IsZero() {
		now = time.Now()
	}
	text := strings.Split(ansi.Hardwrap(instructionText(pane.LastInstruction), maxInt(1, width), true), "\n")
	count := minInt(available-1, len(text))
	lines := []string{mutedStyle.Render("── 마지막 지시 · " + instructionAge(now, pane.LastInstructionAt) + " · p 전체 보기 ──")}
	if count < len(text) {
		text[count-1] = ansi.Truncate(text[count-1], maxInt(1, width-1), "") + "…"
	}
	return append(lines, text[:count]...)
}

func (m Model) openInstruction() (tea.Model, tea.Cmd) {
	if !m.loaded || len(m.panelIndices(m.focusPinned)) == 0 || m.selected < 0 || m.selected >= len(m.rows) {
		return m, nil
	}
	pane := m.rows[m.selected].Pane
	m.instructionOpen = true
	m.instructionContent, m.instructionAt = pane.LastInstruction, pane.LastInstructionAt
	m.instructionViewport = viewport.New(1, 1)
	m.resizeInstruction()
	return m, nil
}

func (m *Model) resizeInstruction() {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	m.instructionViewport.Width = maxInt(1, width-6)
	m.instructionViewport.Height = maxInt(1, height-7)
	text := instructionText(m.instructionContent)
	if text == "" {
		text = "아직 저장된 지시가 없습니다"
	}
	m.instructionViewport.SetContent(ansi.Hardwrap(text, m.instructionViewport.Width, true))
}

func (m Model) updateInstructionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "p", "esc", "q":
		m.instructionOpen = false
		return m, nil
	case "home":
		m.instructionViewport.GotoTop()
		return m, nil
	case "end":
		m.instructionViewport.GotoBottom()
		return m, nil
	case "ctrl+c":
		m.closeStream()
		m.quitting = true
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.instructionViewport, cmd = m.instructionViewport.Update(msg)
	return m, cmd
}

func (m Model) instructionPopupView() string {
	now := m.activityNow
	if now.IsZero() {
		now = time.Now()
	}
	lines := []string{"마지막 지시 · " + instructionAge(now, m.instructionAt), m.instructionViewport.View(), "↑/↓ j/k PgUp/PgDn 스크롤 · p/Esc 닫기"}
	for i := range lines {
		lines[i] = strings.Join(truncateInstructionLines(lines[i], m.instructionViewport.Width), "\n")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(m.instructionViewport.Width + 2).Render(strings.Join(lines, "\n"))
}

func truncateInstructionLines(text string, width int) []string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return lines
}
