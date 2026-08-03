package listselector

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type agent struct {
	name     string
	command  string
	baseArgs []string
	yoloArgs []string
}

var agents = []agent{
	{name: "agent", command: "zellij-agent", baseArgs: []string{"agent", "start", "cursor", "--"}},
	{name: "antigravity", command: "zellij-agent", baseArgs: []string{"agent", "start", "gemini", "--"}},
	{name: "codex", command: "zellij-agent", baseArgs: []string{"agent", "start", "codex", "--"}},
	{name: "claude", command: "claude", yoloArgs: []string{"--dangerously-skip-permissions"}},
}

type focusArea int

const (
	focusAgents focusArea = iota
	focusPrompt
	focusYolo
)

type commandDoneMsg struct {
	err error
}

// Model is the Bubble Tea model for selecting and starting a coding agent.
type Model struct {
	agents     []agent
	cursor     int
	focus      focusArea
	prompt     textinput.Model
	yolo       bool
	status     string
	commandErr error
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	focusedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sectionStyle  = lipgloss.NewStyle().MarginTop(1)
	selectedStyle = lipgloss.NewStyle().Bold(true)
)

// NewModel constructs a selector with the default agent and yolo mode selected.
func NewModel() Model {
	input := textinput.New()
	input.Placeholder = "Initial prompt, optional"
	input.CharLimit = 2048
	input.Width = 70
	input.Blur()

	return Model{
		agents: agents,
		focus:  focusAgents,
		prompt: input,
		yolo:   true,
		status: "Use Tab to move focus. Enter runs from prompt or yolo.",
	}
}

// ResultError returns the child command error recorded by a completed selector.
func ResultError(final tea.Model) error {
	m, ok := final.(interface{ ResultError() error })
	if !ok {
		return nil
	}
	return m.ResultError()
}

// ResultError returns the child command error stored in the model.
func (m Model) ResultError() error {
	return m.commandErr
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "tab":
			m.focusNext()
			return m, nil
		case "shift+tab":
			m.focusPrev()
			return m, nil
		case "ctrl+r":
			return m.runSelectedAgent()
		}

		switch m.focus {
		case focusAgents:
			return m.updateAgentList(msg)
		case focusPrompt:
			if msg.String() == "enter" {
				return m.runSelectedAgent()
			}

			var cmd tea.Cmd
			m.prompt, cmd = m.prompt.Update(msg)
			return m, cmd
		case focusYolo:
			return m.updateYolo(msg)
		}
	case commandDoneMsg:
		m.commandErr = msg.err
		return m, tea.Quit
	}

	return m, nil
}

func (m *Model) focusNext() {
	m.setFocus((m.focus + 1) % 3)
}

func (m *Model) focusPrev() {
	m.setFocus((m.focus + 2) % 3)
}

func (m *Model) setFocus(next focusArea) {
	m.focus = next
	if m.focus == focusPrompt {
		m.prompt.Focus()
		return
	}

	m.prompt.Blur()
}

func (m Model) updateAgentList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.agents)-1 {
			m.cursor++
		}
	case "enter":
		return m.runSelectedAgent()
	case " ":
		m.setFocus(focusPrompt)
	}

	return m, nil
}

func (m Model) updateYolo(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case " ":
		m.yolo = !m.yolo
	case "enter":
		return m.runSelectedAgent()
	case "up", "k":
		m.setFocus(focusPrompt)
	}

	return m, nil
}

func (m Model) selectedCommand() (string, []string) {
	selected := m.agents[m.cursor]
	args := make([]string, 0, len(selected.baseArgs)+len(selected.yoloArgs)+1)
	args = append(args, selected.baseArgs...)
	if m.yolo {
		args = append(args, selected.yoloArgs...)
	}
	if prompt := strings.TrimSpace(m.prompt.Value()); prompt != "" {
		args = append(args, prompt)
	}
	return selected.command, args
}

func (m Model) runSelectedAgent() (tea.Model, tea.Cmd) {
	command, args := m.selectedCommand()
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.status = fmt.Sprintf("Running: %s %s", command, strings.Join(args, " "))

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return commandDoneMsg{err: err}
	})
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Agent Selector"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("ctrl+c/esc: quit | tab: focus | ctrl+r: run"))
	b.WriteString("\n")

	b.WriteString(sectionStyle.Render(m.renderAgents()))
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render(m.renderPrompt()))
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render(m.renderYolo()))

	if m.status != "" {
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render(m.status))
	}

	return b.String()
}

func (m Model) renderAgents() string {
	var b strings.Builder
	label := "Agents"
	if m.focus == focusAgents {
		label = focusedStyle.Render(label)
	}
	b.WriteString(label)
	b.WriteString("\n")

	for i, item := range m.agents {
		cursor := "  "
		if m.cursor == i {
			cursor = "> "
		}

		name := item.name
		if m.cursor == i {
			name = selectedStyle.Render(name)
		}

		b.WriteString(cursor)
		b.WriteString(name)
		if m.yolo && m.cursor == i && len(item.yoloArgs) == 0 {
			b.WriteString(mutedStyle.Render(" (no yolo args configured)"))
		}
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderPrompt() string {
	label := "Init prompt"
	if m.focus == focusPrompt {
		label = focusedStyle.Render(label)
	}

	return label + "\n" + m.prompt.View()
}

func (m Model) renderYolo() string {
	label := "Yolo mode"
	if m.focus == focusYolo {
		label = focusedStyle.Render(label)
	}

	box := "[ ]"
	if m.yolo {
		box = "[x]"
	}

	selected := m.agents[m.cursor]
	hint := "Space toggles"
	if len(selected.yoloArgs) > 0 {
		hint = "adds: " + strings.Join(selected.yoloArgs, " ")
	} else if m.yolo {
		hint = "enabled, but no args configured for " + selected.name
	}

	return fmt.Sprintf("%s\n%s %s %s", label, box, selected.name, mutedStyle.Render(hint))
}
