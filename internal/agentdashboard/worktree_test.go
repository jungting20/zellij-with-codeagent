package agentdashboard

import (
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"zellij-with-codeagent/internal/transport"
)

type worktreeFakeClient struct {
	*fakeClient
	request transport.StartAgentRequest
}

func (c *worktreeFakeClient) StartAgent(_ context.Context, r transport.StartAgentRequest) (transport.StartAgentResponse, error) {
	c.request = r
	return transport.StartAgentResponse{}, nil
}
func TestWorktreePickerLaunchAndCancel(t *testing.T) {
	c := &worktreeFakeClient{fakeClient: &fakeClient{}}
	m := inputModel(t, c.fakeClient, false)
	m.client = c
	m.worktreeParent = m.rows[0]
	m.worktreeParent.Pane.ID = "parent"
	m.worktreeParent.Pane.SessionID = "source"
	m.worktreeParent.Pane.ZellijPaneID = "1"
	next, _ := m.Update(worktreeCreatedMsg{path: "/tmp/project-123456"})
	m = concreteModel(t, next)
	if !strings.Contains(m.View(), "Agent Selector") {
		t.Fatal("selector absent")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, next)
	if !m.worktreeBusy || cmd == nil {
		t.Fatal("launch not guarded")
	}
	next, cmd = m.Update(cmd())
	m = concreteModel(t, next)
	next, _ = m.Update(cmd())
	m = concreteModel(t, next)
	if c.request.TargetSession != "worktree-agent" || c.request.SourceSession != "source" || c.request.ParentPaneID != "parent" || c.request.CWD != "/tmp/project-123456" || c.request.Kind != "codex" || m.worktreePicker != nil {
		t.Fatalf("request=%+v", c.request)
	}
	next, _ = m.Update(worktreeCreatedMsg{path: "/tmp/retained"})
	m = concreteModel(t, next)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = concreteModel(t, next)
	if m.quitting || m.worktreePicker != nil || !strings.Contains(m.statusText, "/tmp/retained") {
		t.Fatal("cancel lost directory")
	}
	next, _ = m.Update(worktreeCreatedMsg{err: errors.New("not git")})
	if !strings.Contains(concreteModel(t, next).statusText, "not git") {
		t.Fatal("error missing")
	}
}
func TestChildOrderingAndMissingParent(t *testing.T) {
	rows := []transport.AgentWithPane{
		{Agent: transport.Agent{ID: "parent"}, Pane: transport.Pane{ID: "p"}},
		{Agent: transport.Agent{ID: "other"}, Pane: transport.Pane{ID: "o"}},
		{Agent: transport.Agent{ID: "child"}, Pane: transport.Pane{ID: "c", ParentPaneID: "p"}},
		{Agent: transport.Agent{ID: "grandchild"}, Pane: transport.Pane{ID: "g", ParentPaneID: "c"}},
	}
	orderChildren(rows)
	if rows[1].Pane.ID != "c" || rows[2].Pane.ID != "g" {
		t.Fatalf("%+v", rows)
	}
	m := Model{rows: rows}
	if m.childLabel(rows[1]) != "  ↳ " || m.childLabel(rows[2]) != "    ↳ " {
		t.Fatal("missing indent")
	}
	m.rows = rows[1:]
	if !strings.Contains(m.childLabel(rows[1]), "[p]") {
		t.Fatal("missing parent label")
	}
}

func TestWorktreeNameBeforePicker(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	c := &worktreeFakeClient{fakeClient: &fakeClient{}}
	m := inputModel(t, c.fakeClient, false)
	m.client = c
	m.rows[0].Pane.CWD = repo
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	m = concreteModel(t, next)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = concreteModel(t, next)
	if !m.worktreeNaming || m.worktreeBusy || m.worktreePicker != nil || !strings.Contains(m.View(), "브랜치명") {
		t.Fatal("expected branch prompt before creation and selection")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, next)
	if cmd != nil || m.worktreeError == "" {
		t.Fatal("empty branch accepted")
	}
	m.worktreePrompt.SetValue("bad name")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, next)
	next, _ = m.Update(cmd())
	m = concreteModel(t, next)
	if !m.worktreeNaming || m.worktreeBusy || m.worktreeError == "" {
		t.Fatal("invalid branch did not return to input")
	}
	branch := filepath.Base(repo) + "-task"
	m.worktreePrompt.SetValue(branch)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, next)
	if _, repeated := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); repeated != nil {
		t.Fatal("duplicate creation allowed")
	}
	next, _ = m.Update(cmd())
	m = concreteModel(t, next)
	if m.worktreeNaming || m.worktreePicker == nil {
		t.Fatalf("selector missing: %s", m.statusText)
	}
	t.Cleanup(func() { exec.Command("git", "-C", repo, "worktree", "remove", "--force", m.worktreePath).Run() })
	out, err := exec.Command("git", "-C", m.worktreePath, "branch", "--show-current").Output()
	if err != nil || !strings.HasPrefix(string(out), branch+"-") {
		t.Fatalf("branch=%s err=%v", out, err)
	}
}

func TestWorktreeNameCancel(t *testing.T) {
	c := &worktreeFakeClient{fakeClient: &fakeClient{}}
	m := inputModel(t, c.fakeClient, false)
	m.client = c
	m.rows[0].Pane.CWD = t.TempDir()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	m = concreteModel(t, next)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = concreteModel(t, next)
	if cmd != nil || m.worktreeNaming || m.worktreeBusy || m.worktreePicker != nil || m.quitting {
		t.Fatal("branch prompt cancellation failed")
	}
}
