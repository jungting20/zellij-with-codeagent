package agentdashboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

func ticketKey(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	return inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func ticketModel(t *testing.T, pinned bool) Model {
	t.Helper()
	m := inputModel(t, &fakeClient{}, pinned)
	m.rows[0].Pane.CWD = t.TempDir()
	m.rows[0].Pane.SessionID = "selected-session"
	m.width, m.height, m.loaded = 80, 24, true
	m, _ = ticketKey(t, m, "t")
	if m.ticket == nil || !strings.Contains(m.View(), "s: ticket start") {
		t.Fatal(m.View())
	}
	return m
}

func TestTicketAddKeepsTargetAndPreventsDuplicate(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		m := ticketModel(t, pinned)
		cwd := m.ticket.target.Pane.CWD
		bin := t.TempDir()
		script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" > args.txt\nprintf '{\"id\":42}'\n"
		if err := os.WriteFile(filepath.Join(bin, "zellij-agent"), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin)
		m, _ = ticketKey(t, m, "a")
		m, empty := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if empty != nil {
			t.Fatal("empty prompt submitted")
		}
		m, _ = ticketKey(t, m, "한글 q s t $literal")
		next, _ := m.Update(refreshResultMsg{agents: transport.ListAgentsResponse{Agents: []transport.AgentWithPane{record("other", "claude", "idle", time.Now())}}})
		m = concreteModel(t, next)
		m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("missing add")
		}
		m, duplicate := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if duplicate != nil {
			t.Fatal("duplicate add")
		}
		m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.ticket == nil {
			t.Fatal("dismissed in-flight add")
		}
		next, _ = m.Update(cmd())
		m = concreteModel(t, next)
		if m.ticket != nil || !strings.Contains(m.statusText, "#42") {
			t.Fatal(m.statusText)
		}
		data, err := os.ReadFile(filepath.Join(cwd, "args.txt"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"ticket-worker\nadd", "--prompt\n한글 q s t $literal", "--agent\ncodex"} {
			if !strings.Contains(string(data), want) {
				t.Fatalf("args %s", data)
			}
		}
	}
}

func TestTicketFailurePreservesDraftAndCancel(t *testing.T) {
	m := ticketModel(t, false)
	m, _ = ticketKey(t, m, "a")
	m, _ = ticketKey(t, m, "retry this")
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	next, _ := m.Update(ticketResultMsg{action: "add", err: errors.New("not initialized")})
	m = concreteModel(t, next)
	if m.ticket.busy || m.ticket.prompt.Value() != "retry this" || !strings.Contains(m.View(), "not initialized") {
		t.Fatal(m.View())
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ticket != nil || m.quitting {
		t.Fatal("cancel failed")
	}
}

func TestTicketStartAndList(t *testing.T) {
	m := ticketModel(t, false)
	if err := os.Mkdir(filepath.Join(m.ticket.target.Pane.CWD, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ticketworker.EnsureConfig(m.ticket.target.Pane.CWD); err != nil {
		t.Fatal(err)
	}
	m, load := ticketKey(t, m, "s")
	if m.ticket.mode != "start" || load == nil {
		t.Fatal("missing agent picker")
	}
	updated, _ := m.Update(load())
	m = concreteModel(t, updated)
	if !m.ticket.agentReady || m.ticket.defaultAgent != "codex" {
		t.Fatal("missing default agent")
	}
	m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.ticket.busy {
		t.Fatal("missing start")
	}
	_, dup := ticketKey(t, m, "s")
	if dup != nil {
		t.Fatal("duplicate start")
	}
	next, _ := m.Update(ticketResultMsg{action: "start"})
	m = concreteModel(t, next)
	if m.ticket != nil {
		t.Fatal("start popup remained")
	}
	m, _ = ticketKey(t, m, "t")
	m, cmd = ticketKey(t, m, "l")
	if cmd == nil || m.ticket.mode != "list" {
		t.Fatal("missing list")
	}
	next, _ = m.Update(ticketResultMsg{action: "list", output: `[{"id":7,"status":"ready","agent":"codex","title":"테스트","prompt":"first\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n16\n17\n18\n19\n20\nlast"}]`})
	m = concreteModel(t, next)
	if !strings.Contains(m.View(), "#7 [ready]") {
		t.Fatal(m.View())
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.ticket.list.YOffset == 0 {
		t.Fatal("list did not scroll")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 35, Height: 12})
	m = concreteModel(t, next)
	for _, line := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(line) > 35 {
			t.Fatal("overflow", line)
		}
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ticket != nil {
		t.Fatal("list not closed")
	}
}

func TestTicketsRequireSelectedProject(t *testing.T) {
	m := inputModel(t, &fakeClient{}, false)
	m.rows[0].Pane.CWD = ""
	m, _ = ticketKey(t, m, "t")
	if m.ticket != nil {
		t.Fatal("opened without working directory")
	}
	m.rows = nil
	m, _ = ticketKey(t, m, "t")
	if m.ticket != nil {
		t.Fatal("opened without selection")
	}
}

func TestTicketStartAlwaysSelectsAgentBeforeExecution(t *testing.T) {
	m := ticketModel(t, false)
	cwd := m.ticket.target.Pane.CWD
	if err := os.Mkdir(filepath.Join(cwd, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ticketworker.EnsureConfig(cwd); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ticketworker.ConfigPath(cwd), []byte("version: 1\ndefault_agent: claude\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "zellij-agent"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > args.txt\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	m, load := ticketKey(t, m, "s")
	next, _ := m.Update(load())
	m = concreteModel(t, next)
	if m.ticket.busy || m.ticket.agentIndex != 1 || !strings.Contains(m.View(), "claude (default_agent)") {
		t.Fatal(m.View())
	}
	if _, err := os.Stat(filepath.Join(cwd, "args.txt")); !os.IsNotExist(err) {
		t.Fatal("started before confirmation")
	}
	// Cancelling and opening start again must show the picker again.
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = ticketKey(t, m, "t")
	m, load = ticketKey(t, m, "s")
	next, _ = m.Update(load())
	m = concreteModel(t, next)
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m, run := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if run == nil {
		t.Fatal("missing start command")
	}
	next, _ = m.Update(run())
	m = concreteModel(t, next)
	if m.ticket != nil {
		t.Fatal(m.View())
	}
	args, err := os.ReadFile(filepath.Join(cwd, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--default-agent\ngemini") {
		t.Fatalf("args: %s", args)
	}
	cfg, err := ticketworker.LoadConfig(cwd)
	if err != nil || cfg.DefaultAgent != "claude" {
		t.Fatal("run selection changed project config")
	}
}

func TestTicketStartConfigFailureDoesNotLaunch(t *testing.T) {
	m := ticketModel(t, false)
	cwd := m.ticket.target.Pane.CWD
	if err := os.Mkdir(filepath.Join(cwd, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ticketworker.EnsureConfig(cwd); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ticketworker.ConfigPath(cwd), []byte("version: 1\nmax_workers: 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m, load := ticketKey(t, m, "s")
	next, _ := m.Update(load())
	m = concreteModel(t, next)
	if m.ticket.err == "" || m.ticket.agentReady {
		t.Fatal("missing config error")
	}
	_, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("started without valid config")
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ticket != nil {
		t.Fatal("cannot cancel")
	}
}

func TestTicketStartMissingConfigAllowsAgentSelection(t *testing.T) {
	m := ticketModel(t, false)
	cwd := m.ticket.target.Pane.CWD
	// Git worktrees use a .git file instead of a directory.
	if err := os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: /unused/worktrees/test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m, load := ticketKey(t, m, "s")
	next, _ := m.Update(load())
	m = concreteModel(t, next)
	if m.ticket.err != "" || !m.ticket.agentReady || m.ticket.defaultAgent != "codex" {
		t.Fatalf("picker = %+v", m.ticket)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".zellij-agent")); !os.IsNotExist(err) {
		t.Fatalf("picker initialized project before start: %v", err)
	}
	_, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("missing start command for uninitialized project")
	}
}
