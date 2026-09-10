package agentdashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"zellij-with-codeagent/internal/transport"
)

func inputModel(t *testing.T, client *fakeClient, pinned bool) Model {
	t.Helper()
	m := concreteModel(t, NewModel(context.Background(), client, Options{}))
	row := record("target", "codex", "idle", time.Now())
	row.Agent.PaneID = "managed-target"
	row.Agent.Pinned = pinned
	row.Pane.Status = "running"
	m.rows, m.focusPinned = []transport.AgentWithPane{row}, pinned
	return m
}

func inputKey(t *testing.T, m Model, key tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(key)
	return concreteModel(t, next), cmd
}

func TestInputKeepsTargetAcrossRefreshAndSendsOnce(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		client := &fakeClient{}
		m := inputModel(t, client, pinned)
		m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
		if !strings.Contains(m.View(), "프롬프트 · target") {
			t.Fatal(m.View())
		}
		m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("한글 q i 프롬프트")})
		next, _ := m.Update(refreshResultMsg{agents: transport.ListAgentsResponse{Agents: []transport.AgentWithPane{record("other", "claude", "idle", time.Now())}}})
		m = concreteModel(t, next)
		m, send := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if send == nil {
			t.Fatal("missing send command")
		}
		m, duplicate := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if duplicate != nil {
			t.Fatal("duplicate send")
		}
		m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.inputPane == "" {
			t.Fatal("dismissed in-flight input")
		}
		next, _ = m.Update(send())
		m = concreteModel(t, next)
		if client.inputCalls != 1 || client.inputPane != "managed-target" || client.inputRequest.Text != "한글 q i 프롬프트\n" {
			t.Fatalf("send = %d %q %+v", client.inputCalls, client.inputPane, client.inputRequest)
		}
		if m.inputPane != "" || m.quitting || client.focusCalls != 0 {
			t.Fatal("send should return to dashboard")
		}
	}
}

func TestInputFailurePreservesPromptForRetry(t *testing.T) {
	client := &fakeClient{inputErr: errors.New("unavailable")}
	m := inputModel(t, client, false)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("retry me")})
	m, send := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	next, _ := m.Update(send())
	m = concreteModel(t, next)
	if m.prompt.Value() != "retry me" || !strings.Contains(m.View(), "unavailable") || m.inputSending {
		t.Fatal(m.View())
	}
	client.inputErr = nil
	m, send = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = m.Update(send())
	if concreteModel(t, next).inputPane != "" || client.inputCalls != 2 {
		t.Fatal("retry failed")
	}
}

func TestInputCancelEmptyAndInactive(t *testing.T) {
	client := &fakeClient{}
	m := inputModel(t, client, false)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m, send := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if send != nil {
		t.Fatal("empty input sent")
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.inputPane != "" || client.inputCalls != 0 {
		t.Fatal("cancel sent input")
	}
	m.rows[0].Pane.Status = "closed"
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if m.inputPane != "" {
		t.Fatal("opened inactive pane")
	}
	m.rows = nil
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if m.inputPane != "" {
		t.Fatal("opened empty selection")
	}
}

func TestInputPopupStaysNearSelectedRowAndWithinScreen(t *testing.T) {
	for _, size := range [][2]int{{80, 20}, {120, 20}, {80, 9}, {30, 8}, {8, 3}} {
		m := inputModel(t, &fakeClient{}, false)
		m.width, m.height, m.loaded = size[0], size[1], true
		base := strings.Split(ansi.Strip(m.View()), "\n")
		m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
		view := ansi.Strip(m.View())
		lines := strings.Split(view, "\n")
		if len(lines) != m.height {
			t.Fatalf("%v: height=%d", size, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > m.width {
				t.Fatalf("%v: overflow %q", size, line)
			}
		}
		if m.height >= 9 {
			if m.height == 20 && strings.TrimRight(lines[0], " ") != strings.TrimRight(base[0], " ") {
				t.Fatalf("dashboard header replaced: %s", view)
			}
			top := -1
			for index, line := range lines {
				if strings.Contains(line, "╭") {
					top = index
					break
				}
			}
			if top < 0 {
				t.Fatalf("missing popup border: %s", view)
			}
			if m.height == 20 && top != m.inputY+1 {
				t.Fatalf("popup not below selection: %s", view)
			}
			if m.height == 9 && top >= m.inputY {
				t.Fatalf("popup not above selection: %s", view)
			}
		}
		if size == [2]int{120, 20} {
			t.Log("\n" + view)
		}
		m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		if strings.Contains(m.View(), "╭") {
			t.Fatal("popup remains after cancel")
		}
	}
}
