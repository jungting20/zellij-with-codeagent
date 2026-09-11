package agentdashboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"zellij-with-codeagent/internal/transport"
)

func fakeNeovim(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nvim"), []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestEditorReadsSavedDraftAndCleansUp(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		fails              bool
	}{
		{"save", "for arg do file=$arg; done\nprintf '첫 줄\\n둘째 줄\\n' > \"$file\"\n", "첫 줄\n둘째 줄", false},
		{"discard", "exit 0\n", "", false},
		{"failure", "exit 7\n", "", true},
		{"missing file", "for arg do file=$arg; done\n/bin/rm \"$file\"\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeNeovim(t, tc.script)
			cmd, finish, err := prepareInputEditor()
			if err != nil {
				t.Fatal(err)
			}
			path := cmd.Args[len(cmd.Args)-1]
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("draft permissions: %v %v", info, err)
			}
			msg := finish(cmd.Run()).(editorResultMsg)
			if (msg.err != nil) != tc.fails || msg.text != tc.want {
				t.Fatalf("result=%+v", msg)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("draft remains: %v", err)
			}
		})
	}
}

func TestShiftIImportsMultilineWithoutSendingAndKeepsTarget(t *testing.T) {
	fakeNeovim(t, "exit 0\n")
	// Keep drafts from unexecuted Bubble Tea commands inside the test directory.
	t.Setenv("TMPDIR", t.TempDir())
	for _, pinned := range []bool{false, true} {
		client := &fakeClient{}
		m := inputModel(t, client, pinned)
		m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
		if cmd == nil || !m.editorRunning || m.inputPane != "managed-target" {
			t.Fatal("editor did not open")
		}
		m, send := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if send != nil {
			t.Fatal("sent during editor execution")
		}
		next, _ := m.Update(refreshResultMsg{agents: transport.ListAgentsResponse{Agents: []transport.AgentWithPane{record("other", "claude", "idle", time.Now())}}})
		m = concreteModel(t, next)
		text := "한글 첫 줄\n둘째 줄\n" + strings.Repeat("긴 프롬프트\n", 150)
		next, _ = m.Update(editorResultMsg{text: text})
		m = concreteModel(t, next)
		if m.editorRunning || m.prompt.Value() != text || client.inputCalls != 0 {
			t.Fatal("draft was changed or sent automatically")
		}
		m, send = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if send == nil {
			t.Fatal("missing send")
		}
		send()
		if client.inputPane != "managed-target" || client.inputRequest.Text != text+"\n" {
			t.Fatalf("send=%+v", client.inputRequest)
		}
	}
}

func TestEditorErrorsAndUnavailableTargets(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	m := inputModel(t, &fakeClient{}, false)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	if m.editorRunning || m.inputPane == "" || m.inputError == "" {
		t.Fatal("missing editor not reported in prompt")
	}
	m.prompt.SetValue("keep me")
	next, _ := m.Update(editorResultMsg{err: errors.New("cancelled")})
	m = concreteModel(t, next)
	if m.prompt.Value() != "keep me" || !strings.Contains(m.inputError, "cancelled") {
		t.Fatal("editor failure lost draft")
	}
	for _, state := range []string{"closed", "empty", "busy"} {
		m := inputModel(t, &fakeClient{}, false)
		switch state {
		case "closed":
			m.rows[0].Pane.Status = "closed"
		case "empty":
			m.rows = nil
		case "busy":
			m.stopping = true
		}
		m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
		if cmd != nil || m.inputPane != "" || m.editorRunning {
			t.Fatalf("opened %s target", state)
		}
	}
}
