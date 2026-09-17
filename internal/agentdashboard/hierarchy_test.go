package agentdashboard

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
	"time"
	"zellij-with-codeagent/internal/transport"
)

func TestDashboardHierarchyAcrossSessionsAndPins(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		parent := record("parent", "codex", "idle", time.Now())
		parent.Pane.ID = "p"
		parent.Pane.SessionID = "project-session"
		parent.Pane.TabID = "source-tab"
		parent.Agent.Pinned = pinned
		parent.Pane.CWD = "/repo/parent"
		child := record("child", "codex", "idle", time.Now())
		child.Pane.ID = "c"
		child.Pane.ParentPaneID = "p"
		child.Pane.SessionID = "worktree-agent"
		child.Pane.TabID = "child-tab"
		child.Pane.CWD = "/tmp/child"
		grandchild := child
		grandchild.Agent.ID = "grandchild"
		grandchild.Pane.ID = "g"
		grandchild.Pane.ParentPaneID = "c"
		grandchild.Pane.CWD = "/tmp/grandchild"
		other := parent
		other.Agent.ID = "other"
		other.Pane.ID = "o"
		other.Pane.CWD = "/repo/other"
		m := inputModel(t, &fakeClient{}, pinned)
		m.rows = []transport.AgentWithPane{grandchild, other, child, parent}
		sortAgentRows(m.rows, "project-session")
		m.loaded = true
		m.width = 120
		m.height = 25
		indices := m.panelIndices(pinned)
		if len(indices) != 4 || len(m.panelIndices(!pinned)) != 0 {
			t.Fatalf("wrong panel: %v", indices)
		}
		var order []string
		parentDisplay := -1
		display := m.displayRows()
		for i, r := range display {
			if r.kind == displaySession && r.session == "worktree-agent" {
				t.Fatal("execution session shown")
			}
			if r.kind == displayAgent {
				id := m.rows[r.agentIndex].Agent.ID
				order = append(order, id)
				if id == "parent" {
					parentDisplay = i
				}
			}
		}
		if parentDisplay < 0 || m.rows[display[parentDisplay+1].agentIndex].Agent.ID != "child" || m.rows[display[parentDisplay+2].agentIndex].Agent.ID != "grandchild" {
			t.Fatalf("not contiguous: %v", order)
		}
		if m.childLabel(child) != "  ↳ " || m.childLabel(grandchild) != "    ↳ " {
			t.Fatal("wrong indentation")
		}
		plain := ansi.Strip(m.View())
		if strings.Contains(plain, "worktree-agent") || strings.Contains(plain, "child-tab") {
			t.Fatal(plain)
		}
		// Numeric selection and movement must use the inherited panel.
		for i, row := range m.rows {
			if row.Agent.ID == "child" {
				m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('1' + i)}})
				if m.selectedID != "child" || m.focusPinned != pinned {
					t.Fatal("wrong child selection")
				}
				m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
				if m.selectedID != "grandchild" {
					t.Fatal("navigation does not follow tree")
				}
				break
			}
		}
	}
}
func TestDashboardOrphanRemainsVisibleWithoutWorktreeSession(t *testing.T) {
	m := inputModel(t, &fakeClient{}, false)
	m.rows[0].Pane.ParentPaneID = "missing-parent"
	m.rows[0].Pane.SessionID = "worktree-agent"
	m.loaded = true
	m.width = 100
	m.height = 20
	view := ansi.Strip(m.View())
	if strings.Contains(view, "worktree-agent") || !strings.Contains(view, "missing-parent") {
		t.Fatal(view)
	}
}
