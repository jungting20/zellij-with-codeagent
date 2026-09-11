package agentdashboard

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"zellij-with-codeagent/cmd/agent-role/prompteditor"
)

type editorResultMsg struct {
	text string
	err  error
}

// prepareInputEditor owns the temporary draft until the process callback runs.
func prepareInputEditor() (*exec.Cmd, func(error) tea.Msg, error) {
	file, err := os.CreateTemp("", "agent-prompt-*.md")
	if err != nil {
		return nil, nil, err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, nil, err
	}
	cmd, err := prompteditor.Command(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, nil, err
	}
	return cmd, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorResultMsg{err: err}
		}
		data, err := os.ReadFile(path)
		// Neovim adds a final line ending; Enter supplies the submission newline.
		text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		return editorResultMsg{text: text, err: err}
	}, nil
}

func (m Model) openInputEditor() (tea.Model, tea.Cmd) {
	if m.gitRunning || m.editorRunning {
		return m, nil
	}
	next, _ := m.openInput()
	m = next.(Model)
	if m.inputPane == "" {
		return m, nil
	}
	cmd, finish, err := prepareInputEditor()
	if err != nil {
		m.inputError = "Neovim 실행 실패: " + err.Error()
		return m, m.prompt.Focus()
	}
	m.editorRunning = true
	m.prompt.Blur()
	return m, tea.ExecProcess(cmd, finish)
}
