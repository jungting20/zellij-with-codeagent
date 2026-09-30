package agentdashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/transport"
)

func TestInstructionPreviewFollowsSelectionAndFitsTerminal(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	rows := []transport.AgentWithPane{
		viewRecord("one", "codex", "idle", "/repo/one", time.Now()),
		viewRecord("two", "claude", "idle", "/repo/two", time.Now()),
	}
	rows[0].Pane.LastInstruction = "로그인 원인을 확인해줘.\n테스트를 실행해줘.\n수정해줘.\n결과를 보고해줘."
	rows[0].Pane.LastInstructionAt = time.Now().Add(-12 * time.Minute)
	rows[1].Pane.LastInstruction = "두 번째 작업"
	m = applyRefresh(t, m, rows)
	for _, width := range []int{20, 80, 120} {
		for _, height := range []int{6, 8, 14, 30} {
			m.width, m.height = width, height
			plain := ansi.Strip(m.View())
			if len(strings.Split(plain, "\n")) > height || !strings.Contains(plain, "> 1 ") {
				t.Fatalf("hidden selection or overflow at %dx%d:\n%s", width, height, plain)
			}
			for _, line := range strings.Split(plain, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("line exceeds width: %q", line)
				}
			}
			if height == 30 && width >= 80 && (!strings.Contains(plain, "12분 전") || !strings.Contains(plain, "로그인") || strings.Contains(plain, "결과를 보고")) {
				t.Fatalf("bad preview:\n%s", plain)
			}
		}
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.View(); !strings.Contains(got, "두 번째 작업") || strings.Contains(got, "로그인") {
		t.Fatalf("wrong selection preview:\n%s", got)
	}
	rows[1].Pane.LastInstruction = "새 요청"
	m = applyRefresh(t, m, rows)
	if !strings.Contains(m.View(), "새 요청") {
		t.Fatal("refresh did not update instruction")
	}
	t.Logf("Dashboard with last instruction:\n%s", ansi.Strip(m.View()))
}

func TestInstructionFullViewScrollsAndResizesWithoutSendingInput(t *testing.T) {
	client := &fakeClient{}
	m := concreteModel(t, NewModel(context.Background(), client, Options{}))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	row := viewRecord("one", "codex", "idle", "/repo/one", time.Now())
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("지시 %02d", i))
	}
	row.Pane.LastInstruction = strings.Join(lines, "\n")
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 16})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if !m.instructionOpen || !strings.Contains(m.View(), "지시 00") {
		t.Fatal("instruction viewer did not open")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(m.View(), "지시 59") {
		t.Fatal("cannot scroll to end")
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 20, Height: 8})
	if len(strings.Split(m.View(), "\n")) > 8 {
		t.Fatal("popup exceeds resized terminal")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.instructionOpen {
		t.Fatal("viewer did not close")
	}
	if client.inputCalls != 0 {
		t.Fatal("viewer sent input")
	}
}

func TestInstructionPresentationRemovesTerminalControls(t *testing.T) {
	got := instructionText("\x1b[31m원인\x1b[0m\r\n\t확인\x00\x07")
	if got != "원인\n 확인" {
		t.Fatalf("presentation = %q", got)
	}
}
