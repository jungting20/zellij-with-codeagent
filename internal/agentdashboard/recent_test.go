package agentdashboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/transport"
)

type recentClient struct {
	*fakeClient
	request transport.StartAgentRequest
	calls   int
}

func (c *recentClient) StartAgent(_ context.Context, req transport.StartAgentRequest) (transport.StartAgentResponse, error) {
	c.calls++
	c.request = req
	return transport.StartAgentResponse{}, nil
}

func TestRecentDirectorySelectionStartsChosenAgentThroughClient(t *testing.T) {
	bin := t.TempDir()
	zoxide := filepath.Join(bin, "zoxide")
	if err := os.WriteFile(zoxide, []byte("#!/bin/sh\nprintf '%s\\n' '/tmp/first project' '/tmp/second project'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	client := &recentClient{fakeClient: &fakeClient{}}
	m := concreteModel(t, NewModel(context.Background(), client, Options{
		SourceSession: "main", SourceZellijPaneID: "42",
	}))
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = concreteModel(t, result)
	if m.recent == nil || cmd == nil {
		t.Fatal("new agent popup did not open")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("open command = %T, want two-command batch", cmd())
	}
	var loaded recentDirectoriesMsg
	for _, part := range batch {
		if msg, ok := part().(recentDirectoriesMsg); ok {
			loaded = msg
		}
	}
	if loaded.err != nil || len(loaded.paths) != 2 {
		t.Fatalf("zoxide result = %#v", loaded)
	}
	m = update(t, m, loaded)
	for _, key := range "second" {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
	}
	if got := m.recent.input.Value(); got != "second" {
		t.Fatalf("search input = %q, want second", got)
	}
	if paths := m.recent.matches(); len(paths) != 1 || paths[0] != "/tmp/second project" {
		t.Fatalf("filtered directories = %#v", paths)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.recent.mode != "kind" || m.recent.path != "/tmp/second project" {
		t.Fatalf("selected directory = %#v", m.recent)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	result, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, result)
	if !m.recent.busy || cmd == nil {
		t.Fatal("agent start command was not queued")
	}
	started, ok := cmd().(recentStartedMsg)
	if !ok || started.err != nil {
		t.Fatalf("agent start result = %#v", started)
	}
	m = update(t, m, started)
	if m.recent != nil || client.calls != 1 {
		t.Fatalf("popup = %#v, calls = %d", m.recent, client.calls)
	}
	if !client.request.NewPane || !client.request.ReuseDirectoryTab || client.request.Kind != "claude" || client.request.CWD != "/tmp/second project" ||
		client.request.SourceSession != "main" || client.request.SourceZellijPaneID != "42" {
		t.Fatalf("start request = %#v", client.request)
	}
}

func TestRecentSearchFiltersAndEmptyResultDoesNotSelect(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &recentClient{fakeClient: &fakeClient{}}, Options{}))
	m.recent = &recentPopup{mode: "directory", input: textinput.New(), directories: []string{"/tmp/alpha", "/tmp/beta"}}
	m.recent.input.SetValue("missing")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.recent.mode != "directory" || m.recent.selected != 0 {
		t.Fatalf("empty search changed selection: %#v", m.recent)
	}
	if !strings.Contains(m.recentView(), "검색 결과가 없습니다") {
		t.Fatal("empty search message missing")
	}
}
