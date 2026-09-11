package agentdashboard

import (
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
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
	if c.request.ParentPaneID != "parent" || c.request.CWD != "/tmp/project-123456" || c.request.Kind != "codex" || m.worktreePicker != nil {
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
