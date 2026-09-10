package agentdashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/transport"
)

func TestAliasPickerAppliesEnumToCapturedAgentInEitherArea(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		client := &fakeClient{}
		m := concreteModel(t, NewModel(context.Background(), client, Options{}))
		row := viewRecord("a", "codex", "working", "/repo/api-server", time.Now())
		row.Agent.Pinned, row.Agent.TaskAlias = pinned, "test"
		m = applyRefresh(t, m, []transport.AgentWithPane{row})
		if pinned {
			m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		if m.aliasTarget != "a" || codingagent.TaskAliases()[m.aliasSelected] != codingagent.TaskAliasTest {
			t.Fatal("picker did not preselect current alias")
		}
		for _, key := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeySpace}, {Type: tea.KeyRunes, Runes: []rune{'d'}}, {Type: tea.KeyRunes, Runes: []rune{'1'}}} {
			next, cmd := m.Update(key)
			m = concreteModel(t, next)
			if cmd != nil || m.selectedID != "a" || m.focusPinned != pinned {
				t.Fatalf("picker leaked key %v", key)
			}
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
		// A list refresh must not retarget the picker.
		other := viewRecord("b", "claude", "idle", "/repo/other", time.Now().Add(-time.Hour))
		other.Agent.Pinned = pinned
		m = applyRefresh(t, m, []transport.AgentWithPane{other, row})
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = concreteModel(t, next)
		if cmd == nil || !m.aliasSaving {
			t.Fatal("Enter did not save")
		}
		_, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if duplicate != nil {
			t.Fatal("duplicate save accepted")
		}
		m = update(t, m, cmd())
		if client.aliasAgentID != "a" || client.aliasRequest.TaskAlias != "review" || m.aliasTarget != "" || m.aliasSaving {
			t.Fatalf("save failed: %#v", client)
		}
		for _, item := range m.rows {
			if item.Agent.ID == "a" && item.Agent.TaskAlias != "review" {
				t.Fatal("alias not reflected in row")
			}
			if item.Agent.ID == "b" && item.Agent.TaskAlias != "" {
				t.Fatal("changed the wrong agent")
			}
		}
	}
}

func TestAliasPickerCancelFailureAndClear(t *testing.T) {
	client := &fakeClient{aliasErr: errors.New("unavailable")}
	m := concreteModel(t, NewModel(context.Background(), client, Options{}))
	row := viewRecord("a", "codex", "working", "/repo/api-server", time.Now())
	row.Agent.TaskAlias = "review"
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	open := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	m = update(t, m, open)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.aliasTarget != "" || client.aliasCalls != 0 || m.rows[0].Agent.TaskAlias != "review" {
		t.Fatal("cancel mutated alias")
	}
	m = update(t, m, open)
	for i := 0; i < 10; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyUp})
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, next)
	m = update(t, m, cmd())
	if m.aliasTarget == "" || m.aliasSaving || !strings.Contains(m.aliasError, "unavailable") || m.rows[0].Agent.TaskAlias != "review" {
		t.Fatal("failure did not keep picker and previous alias")
	}
	client.aliasErr = nil
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, next)
	m = update(t, m, cmd())
	if m.aliasTarget != "" || m.rows[0].Agent.TaskAlias != "" || client.aliasRequest.TaskAlias != "" {
		t.Fatal("clear did not remove alias")
	}
}

func TestAliasPickerAndBadgeFitSmallWindows(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	row := viewRecord("a", "codex", "idle", "/repo/api-server", time.Now())
	row.Agent.TaskAlias = "review"
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	m.width, m.height = 120, 16
	if !strings.Contains(ansi.Strip(m.View()), "[리뷰] api-server") {
		t.Fatalf("missing alias badge:\n%s", m.View())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 14}, {Width: 20, Height: 6}} {
		m = update(t, m, size)
		plain := ansi.Strip(m.View())
		if !strings.Contains(plain, "> 리뷰") || len(strings.Split(plain, "\n")) > size.Height {
			t.Fatalf("picker selection hidden:\n%s", plain)
		}
		for _, line := range strings.Split(plain, "\n") {
			if ansi.StringWidth(line) > size.Width {
				t.Fatal("picker width overflow")
			}
		}
	}
}
