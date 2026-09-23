package agentdashboard

import (
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"zellij-with-codeagent/cmd/agent-role/agentworktreesend"
	"zellij-with-codeagent/internal/transport"
)

func TestWorktreeMenuShellAndCancel(t *testing.T) {
	m := inputModel(t, &fakeClient{}, false)
	m.rows[0].Pane.CWD = "/repo"
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.worktrees == nil || m.worktreeNaming || !strings.Contains(m.View(), "m: merge") || !strings.Contains(m.View(), "g: lazygit") || !strings.Contains(m.View(), "t: 자식 워크트리 탭으로 이동") {
		t.Fatal(m.View())
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.worktrees.mode != "send" {
		t.Fatal("missing shell prompt")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("blank executed")
	}
	m.worktrees.prompt.SetValue("pwd")
	m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.worktrees.busy {
		t.Fatal("execution missing")
	}
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("duplicate execution")
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.worktrees == nil {
		t.Fatal("dismissed running command")
	}
	next, _ := m.Update(worktreeShellResultMsg{results: []agentworktreesend.Result{{Path: "/repo", Output: "ok"}, {Path: "/child", Error: "exit status 1"}}})
	m = concreteModel(t, next)
	if !strings.Contains(m.View(), "1 성공 / 1 실패") || !strings.Contains(m.View(), "exit status 1") {
		t.Fatal(m.View())
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.worktrees != nil || m.quitting {
		t.Fatal("cancel failed")
	}
}
func TestWorktreeMenuMergeUsesExistingFlow(t *testing.T) {
	m := inputModel(t, &fakeClient{}, false)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.worktrees != nil || !strings.Contains(m.statusText, "자식 worktree agent가 없습니다") {
		t.Fatal(m.statusText)
	}
}

func TestWorktreeShellKeepsSelectedParentAcrossRefresh(t *testing.T) {
	parentDir, childDir, otherDir := t.TempDir(), t.TempDir(), t.TempDir()
	client := &fakeClient{}
	m := inputModel(t, client, false)
	m.rows[0].Pane.ID = "parent-pane"
	m.rows[0].Pane.CWD = parentDir
	parent := m.rows[0]
	client.listResponse = transport.ListAgentsResponse{Agents: []transport.AgentWithPane{
		parent,
		{Agent: transport.Agent{ID: "child"}, Pane: transport.Pane{ID: "child-pane", ParentPaneID: "parent-pane", CWD: childDir, Status: "running"}},
		{Agent: transport.Agent{ID: "other"}, Pane: transport.Pane{ID: "other-pane", CWD: otherDir, Status: "running"}},
	}}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m.rows = []transport.AgentWithPane{client.listResponse.Agents[2]}
	m.worktrees.prompt.SetValue("printf done > marker")
	m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	next, _ := m.Update(cmd())
	m = concreteModel(t, next)
	if !strings.Contains(m.worktrees.summary, "1 성공 / 0 실패") {
		t.Fatal(m.worktrees.summary)
	}
	if _, err := os.Stat(filepath.Join(childDir, "marker")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{parentDir, otherDir} {
		if _, err := os.Stat(filepath.Join(path, "marker")); !os.IsNotExist(err) {
			t.Fatal("executed outside selected children")
		}
	}
}

func TestWorktreeGoFocusesDirectChildAndGuardsRepeats(t *testing.T) {
	client := &fakeClient{}
	m := inputModel(t, client, false)
	m.opts.SourceSession = "dashboard-session"
	m.opts.SourceZellijPaneID = "terminal_10"
	m.rows[0].Pane.ID = "parent-pane"
	m.rows = append(m.rows,
		transport.AgentWithPane{Agent: transport.Agent{ID: "closed"}, Pane: transport.Pane{ParentPaneID: "parent-pane", SessionID: "worktree-agent", Status: "closed"}},
		transport.AgentWithPane{Agent: transport.Agent{ID: "unrelated"}, Pane: transport.Pane{ParentPaneID: "other", SessionID: "worktree-agent", Status: "running"}},
		transport.AgentWithPane{Agent: transport.Agent{ID: "child"}, Pane: transport.Pane{ID: "child-pane", ParentPaneID: "parent-pane", SessionID: "worktree-agent", Status: "running"}},
	)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m.selected = 2 // Refresh/selection changes must not change the menu's parent.
	m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if cmd == nil || !m.focusing || !m.worktrees.busy {
		t.Fatal("missing focus command")
	}
	if _, repeat := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}); repeat != nil {
		t.Fatal("duplicate focus")
	}
	next, _ := m.Update(cmd())
	m = concreteModel(t, next)
	if client.focusAgentID != "child" || client.focusCalls != 1 || client.focusRequest.SourceSession != "dashboard-session" || client.focusRequest.SourceZellijPaneID != "terminal_10" || !m.quitting {
		t.Fatalf("focus=%+v model=%+v", client, m)
	}
}
func TestWorktreeGoWithoutChildKeepsMenu(t *testing.T) {
	m := inputModel(t, &fakeClient{}, false)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if cmd != nil || m.worktrees == nil || m.quitting || !strings.Contains(m.View(), "이동할 자식") {
		t.Fatal(m.View())
	}
}
func TestWorktreeGoFailureAllowsRetry(t *testing.T) {
	m := inputModel(t, &fakeClient{}, false)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m.focusing = true
	m.worktrees.busy = true
	next, _ := m.Update(focusResultMsg{err: errors.New("pane closed")})
	m = concreteModel(t, next)
	if m.focusing || m.worktrees.busy || m.quitting || !strings.Contains(m.View(), "pane closed") {
		t.Fatal(m.View())
	}
}
