package listselector

import (
	"errors"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDefaultAgentCommands(t *testing.T) {
	tests := []struct {
		name        string
		yolo        bool
		prompt      string
		wantCommand string
		wantArgs    []string
	}{
		{name: "agent", yolo: true, wantCommand: "zellij-agent", wantArgs: []string{"agent", "start", "agent"}},
		{name: "antigravity", yolo: true, prompt: "fix it", wantCommand: "zellij-agent", wantArgs: []string{"agent", "start", "agy", "--dangerously-skip-permissions", "fix it"}},
		{name: "codex", yolo: true, wantCommand: "zellij-agent", wantArgs: []string{"agent", "start", "codex", "--dangerously-bypass-approvals-and-sandbox"}},
		{name: "claude", yolo: false, prompt: "review", wantCommand: "claude", wantArgs: []string{"review"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel()
			m.yolo = tt.yolo
			m.prompt.SetValue(tt.prompt)
			for index, item := range m.agents {
				if item.name == tt.name {
					m.cursor = index
					break
				}
			}

			command, args := m.selectedCommand()
			if command != tt.wantCommand {
				t.Fatalf("command = %q, want %q", command, tt.wantCommand)
			}
			if !reflect.DeepEqual(args, tt.wantArgs) {
				t.Fatalf("args = %#v, want %#v", args, tt.wantArgs)
			}
		})
	}
}

func TestAgentsExcludeRemovedCommands(t *testing.T) {
	m := NewModel()
	for _, item := range m.agents {
		if item.name == "opencode" || item.name == "pi" {
			t.Fatalf("agents includes removed command %q", item.name)
		}
	}
}

func TestNewModelDefaults(t *testing.T) {
	m := NewModel()
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.cursor)
	}
	if m.focus != focusAgents {
		t.Fatalf("focus = %d, want agents", m.focus)
	}
	if !m.yolo {
		t.Fatal("yolo = false, want true")
	}
	if m.prompt.Placeholder != "Initial prompt, optional" {
		t.Fatalf("placeholder = %q", m.prompt.Placeholder)
	}
	if m.prompt.CharLimit != 2048 {
		t.Fatalf("char limit = %d, want 2048", m.prompt.CharLimit)
	}
}

func TestAgentListNavigationAndPromptFocus(t *testing.T) {
	m := NewModel()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.cursor)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor after up = %d, want 0", m.cursor)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	if m.focus != focusPrompt {
		t.Fatalf("focus after space = %d, want prompt", m.focus)
	}
}

func TestResultErrorReturnsChildFailure(t *testing.T) {
	want := errors.New("child failed")
	m := NewModel()
	m.commandErr = want

	if got := ResultError(m); !errors.Is(got, want) {
		t.Fatalf("ResultError() = %v, want %v", got, want)
	}
}
