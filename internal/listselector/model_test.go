package listselector

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDefaultAgentCommands(t *testing.T) {
	tests := []struct {
		name        string
		yolo        bool
		voice       bool
		prompt      string
		wantCommand string
		wantArgs    []string
	}{
		{name: "agent", yolo: true, wantCommand: "zellij-agent", wantArgs: []string{"agent", "start", "cursor", "--"}},
		{name: "antigravity", yolo: true, voice: true, prompt: "fix it", wantCommand: "zellij-agent", wantArgs: []string{"agent", "start", "gemini", "--notify-idle", "--", "fix it"}},
		{name: "codex", yolo: true, wantCommand: "zellij-agent", wantArgs: []string{"agent", "start", "codex", "--"}},
		{name: "hermes", yolo: true, voice: true, prompt: "investigate", wantCommand: "zellij-agent", wantArgs: []string{"agent", "start", "hermes", "--", "chat", "--yolo", "-q", "investigate"}},
		{name: "claude", yolo: false, voice: true, prompt: "review", wantCommand: "zellij-agent", wantArgs: []string{"agent", "start", "claude", "--notify-idle", "--", "review"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel()
			m.yolo = tt.yolo
			m.voice = tt.voice
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

func TestDefaultAgentOrder(t *testing.T) {
	m := NewModel()
	want := []string{"codex", "hermes", "agent", "antigravity", "claude"}
	got := make([]string, 0, len(m.agents))
	for _, item := range m.agents {
		got = append(got, item.name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent order = %#v, want %#v", got, want)
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
	if m.voice {
		t.Fatal("voice = true, want false")
	}
	if m.prompt.Placeholder != "Initial prompt, optional" {
		t.Fatalf("placeholder = %q", m.prompt.Placeholder)
	}
	if m.prompt.CharLimit != 2048 {
		t.Fatalf("char limit = %d, want 2048", m.prompt.CharLimit)
	}
}

func TestVoiceModeFocusAndToggle(t *testing.T) {
	m := NewModel()
	m.setFocus(focusVoice)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	if !m.voice {
		t.Fatal("voice = false after space, want true")
	}

	view := m.View()
	if !strings.Contains(view, "Voice mode") || !strings.Contains(view, "[x] notify when agent becomes idle") {
		t.Fatalf("View() missing enabled voice checkbox: %q", view)
	}
	if strings.Index(view, "Voice mode") > strings.Index(view, "Yolo mode") {
		t.Fatalf("View() renders Voice mode after Yolo mode: %q", view)
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
