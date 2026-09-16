package agentdashboard

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"zellij-with-codeagent/internal/transport"
)

func mergeModel(t *testing.T) (Model, *fakeClient) {
	t.Helper()
	client := &fakeClient{}
	m := inputModel(t, client, false)
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	git("init", "-b", "main")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init")
	childPath := filepath.Join(t.TempDir(), "child")
	git("worktree", "add", "-b", "feat/child", childPath)
	m.rows[0].Pane.ID = "managed-target"
	m.rows[0].Pane.CWD = repo
	child := record("child", "codex", "idle", time.Now())
	child.Pane.ID = "managed-child"
	child.Pane.Status = "running"
	child.Pane.ParentPaneID = "managed-target"
	child.Pane.CWD = childPath
	m.rows = append(m.rows, child)
	client.listResponse.Agents = append([]transport.AgentWithPane(nil), m.rows...)
	return m, client
}

func TestMergeSendsToParentOnceAcrossRefresh(t *testing.T) {
	m, client := mergeModel(t)
	m, send := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if send == nil {
		t.Fatal("missing request")
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("m")}, {Type: tea.KeyEnter}, {Type: tea.KeyEsc}} {
		var duplicate tea.Cmd
		m, duplicate = inputKey(t, m, key)
		if duplicate != nil || !m.mergeBusy {
			t.Fatal("in-flight request disturbed")
		}
	}
	next, _ := m.Update(refreshResultMsg{agents: transport.ListAgentsResponse{Agents: []transport.AgentWithPane{m.rows[1]}}})
	m = concreteModel(t, next)
	next, _ = m.Update(send())
	m = concreteModel(t, next)
	if client.inputCalls != 1 || client.inputPane != "managed-target" || !strings.Contains(client.inputRequest.Text, "feat/child") || !strings.Contains(client.inputRequest.Text, "main") {
		t.Fatalf("wrong request: %+v", client)
	}
	if m.mergeParent != "" || m.statusText != "merge 요청 전송됨" {
		t.Fatalf("%+v", m)
	}
}

func TestMergeRevalidatesBeforeSending(t *testing.T) {
	for _, scenario := range []string{"working-child", "working-parent", "removed", "unrelated", "list-error", "send-error"} {
		t.Run(scenario, func(t *testing.T) {
			m, client := mergeModel(t)
			m, send := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
			switch scenario {
			case "working-child":
				client.listResponse.Agents[1].Agent.State = "working"
			case "working-parent":
				client.listResponse.Agents[0].Agent.State = "working"
			case "removed":
				client.listResponse.Agents = nil
			case "unrelated":
				client.listResponse.Agents[1].Pane.ParentPaneID = "other"
			case "list-error":
				client.listErr = errors.New("offline")
			case "send-error":
				client.inputErr = errors.New("offline")
			}
			next, _ := m.Update(send())
			m = concreteModel(t, next)
			if !strings.Contains(m.statusText, "실패") || m.mergeBusy {
				t.Fatal(m.statusText)
			}
			if scenario != "send-error" && client.inputCalls != 0 {
				t.Fatal("sent invalid request")
			}
		})
	}
}

func TestMergePickerAndGuards(t *testing.T) {
	m, client := mergeModel(t)
	other := m.rows[1]
	other.Agent.ID = "second"
	m.rows = append(m.rows, other)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.mergeParent == "" || !strings.Contains(m.mergeView(), "second") {
		t.Fatal("missing picker")
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.mergeSelected != 1 {
		t.Fatal("selection did not move")
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mergeParent != "" || client.inputCalls != 0 {
		t.Fatal("cancel sent input")
	}
	m.rows = m.rows[:2]
	m.rows[1].Agent.State = "working"
	m, send := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if send != nil || !strings.Contains(m.statusText, "idle") {
		t.Fatal("working child accepted")
	}
	m.rows = m.rows[:1]
	m, send = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if send != nil || !strings.Contains(m.statusText, "없습니다") {
		t.Fatal("missing child accepted")
	}
}
