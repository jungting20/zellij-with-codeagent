package agentdashboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLazygitUsesSelectedPaneDirectoryAndReturns(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lazygit"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, pinned := range []bool{false, true} {
		m := inputModel(t, &fakeClient{}, pinned)
		dir := t.TempDir()
		m.rows[0].Pane.CWD = dir
		cmd, err := m.lazygitCommand()
		if err != nil || cmd.Dir != dir {
			t.Fatalf("command=%v error=%v", cmd, err)
		}
		m, run := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		if run == nil || !m.gitRunning {
			t.Fatal("g did not launch lazygit")
		}
		next, duplicate := m.openLazygit()
		if duplicate != nil {
			t.Fatal("duplicate launch")
		}
		m = concreteModel(t, next)
		next, _ = m.Update(lazygitResultMsg{})
		m = concreteModel(t, next)
		if m.gitRunning || m.quitting || m.rows[0].Pane.CWD != dir {
			t.Fatal("did not return to dashboard")
		}
		next, _ = m.Update(lazygitResultMsg{err: errors.New("exit 1")})
		if !strings.Contains(concreteModel(t, next).statusText, "exit 1") {
			t.Fatal("missing error")
		}
	}
}

func TestLazygitMissingDirectoryAndBinary(t *testing.T) {
	m := inputModel(t, &fakeClient{}, false)
	m.rows[0].Pane.CWD = ""
	next, cmd := m.openLazygit()
	if cmd != nil || !strings.Contains(concreteModel(t, next).statusText, "failed") {
		t.Fatal("accepted empty cwd")
	}
	m.rows[0].Pane.CWD = t.TempDir()
	t.Setenv("PATH", t.TempDir())
	next, cmd = m.openLazygit()
	if cmd != nil || !strings.Contains(concreteModel(t, next).statusText, "failed") {
		t.Fatal("accepted missing executable")
	}
	m.rows = nil
	if _, err := m.lazygitCommand(); err == nil {
		t.Fatal("accepted empty selection")
	}
}
