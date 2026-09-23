package agentdashboard

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/transport"
)

var recentAgentKinds = []string{"codex", "claude", "gemini", "cursor", "hermes"}

type recentPopup struct {
	mode        string
	input       textinput.Model
	directories []string
	selected    int
	path        string
	kind        int
	busy        bool
	err         string
}

type recentDirectoriesMsg struct {
	popup *recentPopup
	paths []string
	err   error
}

type recentStartedMsg struct {
	popup *recentPopup
	path  string
	kind  string
	err   error
}

func (m Model) openRecent() (tea.Model, tea.Cmd) {
	if m.stopping || m.pinning || m.focusing || m.gitRunning || m.worktreeBusy {
		return m, nil
	}
	if _, ok := m.client.(worktreeClient); !ok {
		m.statusText = "agent start failed: launch unavailable"
		return m, nil
	}
	input := textinput.New()
	input.Placeholder = "디렉토리 검색"
	input.CharLimit = 0
	input.Width = 62
	p := &recentPopup{mode: "directory", input: input, busy: true}
	m.recent = p
	return m, tea.Batch(p.input.Focus(), m.recentDirectoriesCmd(p))
}

func (m Model) recentDirectoriesCmd(p *recentPopup) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "zoxide", "query", "--list").Output()
		if err != nil {
			return recentDirectoriesMsg{popup: p, err: fmt.Errorf("zoxide query: %w", err)}
		}
		paths := make([]string, 0)
		for _, path := range strings.Split(string(output), "\n") {
			if path != "" {
				paths = append(paths, path)
			}
		}
		return recentDirectoriesMsg{popup: p, paths: paths}
	}
}

func (m Model) handleRecentDirectories(msg recentDirectoriesMsg) (tea.Model, tea.Cmd) {
	if m.recent != msg.popup {
		return m, nil
	}
	m.recent.busy = false
	if msg.err != nil {
		m.recent.err = msg.err.Error()
		return m, nil
	}
	m.recent.directories = msg.paths
	if len(msg.paths) == 0 {
		m.recent.err = "zoxide에 저장된 디렉토리가 없습니다"
	}
	return m, nil
}

func (p *recentPopup) matches() []string {
	query := strings.ToLower(strings.TrimSpace(p.input.Value()))
	if query == "" {
		return p.directories
	}
	var paths []string
	for _, path := range p.directories {
		if strings.Contains(strings.ToLower(path), query) {
			paths = append(paths, path)
		}
	}
	return paths
}

func (m Model) updateRecentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.recent
	if p.busy && p.mode == "kind" {
		return m, nil
	}
	if msg.String() == "esc" || msg.String() == "ctrl+c" {
		m.recent = nil
		return m, nil
	}
	if p.busy {
		return m, nil
	}
	if p.mode == "directory" {
		paths := p.matches()
		switch msg.String() {
		case "up":
			p.selected = maxInt(0, p.selected-1)
			return m, nil
		case "down":
			if len(paths) > 0 {
				p.selected = minInt(len(paths)-1, p.selected+1)
			}
			return m, nil
		case "enter":
			if len(paths) == 0 {
				return m, nil
			}
			p.path = paths[p.selected]
			p.mode = "kind"
			p.err = ""
			p.input.Blur()
			return m, nil
		}
		previous := p.input.Value()
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		if previous != p.input.Value() {
			p.selected = 0
		}
		return m, cmd
	}
	if p.mode == "kind" {
		switch msg.String() {
		case "up", "k":
			p.kind = maxInt(0, p.kind-1)
		case "down", "j":
			p.kind = minInt(len(recentAgentKinds)-1, p.kind+1)
		case "enter":
			p.busy = true
			p.err = ""
			return m, m.startRecentCmd(p)
		case "backspace":
			p.mode = "directory"
			return m, p.input.Focus()
		}
	}
	return m, nil
}

func (m Model) startRecentCmd(p *recentPopup) tea.Cmd {
	return func() tea.Msg {
		kind := recentAgentKinds[p.kind]
		path := p.path
		client := m.client.(worktreeClient)
		_, err := client.StartAgent(m.ctx, transport.StartAgentRequest{
			NewPane:            true,
			Kind:               kind,
			CWD:                path,
			SourceSession:      m.opts.SourceSession,
			SourceZellijPaneID: m.opts.SourceZellijPaneID,
		})
		return recentStartedMsg{popup: p, path: path, kind: kind, err: err}
	}
}

func (m Model) handleRecentStarted(msg recentStartedMsg) (tea.Model, tea.Cmd) {
	if m.recent != msg.popup {
		return m, nil
	}
	m.recent.busy = false
	if msg.err != nil {
		m.recent.err = msg.err.Error()
		return m, nil
	}
	m.recent = nil
	m.statusText = "started " + msg.kind + " in " + msg.path
	return m, m.requestRefresh()
}

func (m Model) recentView() string {
	p := m.recent
	width := 72
	if m.width > 0 {
		width = minInt(width, maxInt(20, m.width-4))
	}
	lines := []string{"새 에이전트"}
	if p.mode == "directory" {
		lines = append(lines, p.input.View())
		paths := p.matches()
		if p.busy {
			lines = append(lines, "zoxide 디렉토리 로딩 중…")
		} else if len(paths) == 0 && p.err == "" {
			lines = append(lines, "검색 결과가 없습니다")
		}
		visible := 10
		if m.height > 0 {
			visible = maxInt(1, minInt(visible, m.height-8))
		}
		start := viewportStart(p.selected, len(paths), visible)
		for index := start; index < len(paths) && index < start+visible; index++ {
			prefix := "  "
			if index == p.selected {
				prefix = "> "
			}
			lines = append(lines, prefix+paths[index])
		}
		lines = append(lines, "↑↓ 선택 · Enter 다음 · Esc 닫기")
	} else {
		lines = append(lines, "디렉토리: "+p.path, "에이전트 종류:")
		for index, kind := range recentAgentKinds {
			prefix := "  "
			if index == p.kind {
				prefix = "> "
			}
			lines = append(lines, prefix+kind)
		}
		lines = append(lines, "↑↓ 선택 · Enter 실행 · Backspace 이전 · Esc 닫기")
		if p.busy {
			lines = append(lines, "에이전트 실행 중…")
		}
	}
	if p.err != "" {
		lines = append(lines, "오류: "+p.err)
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], maxInt(1, width-4), "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).
		Width(maxInt(1, width-2)).Render(strings.Join(lines, "\n"))
}
