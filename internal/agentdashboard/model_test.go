package agentdashboard

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/transport"
)

type fakeClient struct {
	listResponse  transport.ListAgentsResponse
	listErr       error
	listCalls     int
	focusResponse transport.FocusAgentResponse
	focusErr      error
	focusCalls    int
	focusAgentID  string
	focusRequest  transport.FocusAgentRequest
	stream        *transport.EventStream
	streamErr     error
	streamCalls   int
	streamTypes   []string
}

func (f *fakeClient) ListAgents(context.Context) (transport.ListAgentsResponse, error) {
	f.listCalls++
	return f.listResponse, f.listErr
}

func (f *fakeClient) FocusAgent(_ context.Context, agentID string, request transport.FocusAgentRequest) (transport.FocusAgentResponse, error) {
	f.focusCalls++
	f.focusAgentID = agentID
	f.focusRequest = request
	return f.focusResponse, f.focusErr
}

func (f *fakeClient) StreamEvents(context.Context) (*transport.EventStream, error) {
	f.streamCalls++
	return f.stream, f.streamErr
}

func (f *fakeClient) StreamEventsByType(_ context.Context, types ...string) (*transport.EventStream, error) {
	f.streamCalls++
	f.streamTypes = append([]string(nil), types...)
	return f.stream, f.streamErr
}

func TestModelInitLoadsAgentsConnectsStreamAndSchedulesPoll(t *testing.T) {
	events := make(chan transport.Event)
	errs := make(chan error)
	client := &fakeClient{
		listResponse: transport.ListAgentsResponse{Agents: []transport.AgentWithPane{record("agent-1", "codex", "idle", time.Unix(10, 0))}},
		stream:       &transport.EventStream{Events: events, Errors: errs},
	}
	m := concreteModel(t, NewModel(context.Background(), client, Options{RefreshInterval: time.Hour}))

	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("Init() message = %T %#v, want three-command BatchMsg", m.Init()(), m.Init()())
	}
	if _, ok := batch[0]().(refreshResultMsg); !ok {
		t.Fatalf("first command returned %T, want refreshResultMsg", batch[0]())
	}
	if _, ok := batch[1]().(streamReadyMsg); !ok {
		t.Fatalf("second command returned %T, want streamReadyMsg", batch[1]())
	}
	if client.listCalls != 1 || client.streamCalls != 1 {
		t.Fatalf("client calls list=%d stream=%d, want 1 each", client.listCalls, client.streamCalls)
	}
	if !reflect.DeepEqual(client.streamTypes, []string{agentStateChangedEventType}) {
		t.Fatalf("stream types = %#v, want agent state changes only", client.streamTypes)
	}
	if batch[2] == nil {
		t.Fatal("poll command is nil")
	}
}

func TestModelKeepsCreationOrderAndSelectionByAgentID(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = applyRefresh(t, m, []transport.AgentWithPane{
		record("agent-late", "claude", "working", time.Unix(30, 0)),
		record("agent-first", "codex", "idle", time.Unix(10, 0)),
		record("agent-middle", "gemini", "blocked", time.Unix(20, 0)),
	})
	if got := rowIDs(m.rows); !reflect.DeepEqual(got, []string{"agent-first", "agent-middle", "agent-late"}) {
		t.Fatalf("row IDs = %#v", got)
	}
	m.selected = 1
	m.selectedID = "agent-middle"

	m = applyRefresh(t, m, []transport.AgentWithPane{
		record("agent-new", "cursor", "idle", time.Unix(5, 0)),
		record("agent-middle", "gemini", "idle", time.Unix(20, 0)),
		record("agent-late", "claude", "working", time.Unix(30, 0)),
	})
	if m.selectedID != "agent-middle" || m.rows[m.selected].Agent.ID != "agent-middle" {
		t.Fatalf("selection index=%d id=%q rows=%#v", m.selected, m.selectedID, rowIDs(m.rows))
	}
}

func TestModelGroupsBySessionWithCurrentSessionFirst(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{SourceSession: "session-b"}))
	records := []transport.AgentWithPane{
		record("a-late", "codex", "idle", time.Unix(40, 0)),
		record("b-late", "claude", "idle", time.Unix(30, 0)),
		record("ungrouped", "gemini", "idle", time.Unix(5, 0)),
		record("b-first", "cursor", "idle", time.Unix(10, 0)),
		record("a-first", "codex", "idle", time.Unix(20, 0)),
	}
	records[0].Pane.SessionID = "session-a"
	records[1].Pane.SessionID = "session-b"
	records[3].Pane.SessionID = "session-b"
	records[4].Pane.SessionID = "session-a"

	m = applyRefresh(t, m, records)
	if got := rowIDs(m.rows); !reflect.DeepEqual(got, []string{"b-first", "b-late", "a-first", "a-late", "ungrouped"}) {
		t.Fatalf("row IDs = %#v", got)
	}
}

func TestModelNavigationClampsAtListBounds(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = applyRefresh(t, m, []transport.AgentWithPane{
		record("a", "codex", "idle", time.Unix(1, 0)),
		record("b", "claude", "idle", time.Unix(2, 0)),
	})

	for _, key := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyRunes, Runes: []rune{'k'}}} {
		m = update(t, m, key)
		if m.selected != 0 {
			t.Fatalf("key %q selected=%d, want 0", key.String(), m.selected)
		}
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyRunes, Runes: []rune{'j'}}, {Type: tea.KeyDown}} {
		m = update(t, m, key)
	}
	if m.selected != 1 || m.selectedID != "b" {
		t.Fatalf("selected=%d id=%q, want b at 1", m.selected, m.selectedID)
	}
}

func TestModelFocusUsesSelectedAgentAndSourceContext(t *testing.T) {
	client := &fakeClient{focusResponse: transport.FocusAgentResponse{Agent: record("agent-2", "claude", "idle", time.Unix(2, 0))}}
	m := concreteModel(t, NewModel(context.Background(), client, Options{SourceSession: "source-session", SourceZellijPaneID: "terminal_9"}))
	m = applyRefresh(t, m, []transport.AgentWithPane{
		record("agent-1", "codex", "idle", time.Unix(1, 0)),
		record("agent-2", "claude", "idle", time.Unix(2, 0)),
	})
	m.selected, m.selectedID = 1, "agent-2"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, next)
	if cmd == nil || !m.focusing {
		t.Fatalf("Enter cmd=%v focusing=%t", cmd, m.focusing)
	}
	if _, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); duplicate != nil {
		t.Fatal("second Enter started a concurrent focus request")
	}
	msg := cmd()
	if client.focusAgentID != "agent-2" || client.focusRequest != (transport.FocusAgentRequest{SourceSession: "source-session", SourceZellijPaneID: "terminal_9"}) {
		t.Fatalf("focus id=%q request=%#v", client.focusAgentID, client.focusRequest)
	}
	m = update(t, m, msg)
	if m.focusing || m.statusText != "focused agent-2" {
		t.Fatalf("focusing=%t status=%q", m.focusing, m.statusText)
	}
}

func TestModelFocusFailureKeepsDashboardAlive(t *testing.T) {
	client := &fakeClient{focusErr: errors.New("target pane disappeared")}
	m := concreteModel(t, NewModel(context.Background(), client, Options{}))
	m = applyRefresh(t, m, []transport.AgentWithPane{record("agent-1", "codex", "idle", time.Unix(1, 0))})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = concreteModel(t, next)
	next, refresh := m.Update(cmd())
	m = concreteModel(t, next)
	if m.statusText != "focus failed: target pane disappeared" || m.quitting {
		t.Fatalf("status=%q quitting=%t", m.statusText, m.quitting)
	}
	if refresh == nil || !m.refreshing {
		t.Fatalf("focus failure refresh=%v refreshing=%t, want immediate refresh", refresh, m.refreshing)
	}
}

func TestModelRefreshTriggersAreCoalescedAndEventFiltered(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m.stream = &transport.EventStream{Events: make(chan transport.Event), Errors: make(chan error)}
	m.refreshing = false

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = concreteModel(t, next)
	if cmd == nil || !m.refreshing {
		t.Fatalf("R cmd=%v refreshing=%t", cmd, m.refreshing)
	}
	if _, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}}); duplicate != nil {
		t.Fatal("second R started a concurrent refresh")
	}

	m.refreshing, m.refreshDirty = false, false
	next, cmd = m.Update(streamEventMsg{event: transport.Event{Type: "agent_state_changed"}})
	m = concreteModel(t, next)
	if cmd == nil || !m.refreshing {
		t.Fatalf("state event cmd=%v refreshing=%t", cmd, m.refreshing)
	}
	m.refreshing, m.refreshDirty = false, false
	next, cmd = m.Update(streamEventMsg{event: transport.Event{Type: "raw_output"}})
	m = concreteModel(t, next)
	if m.refreshing || m.refreshDirty {
		t.Fatalf("unrelated event requested refresh: %#v", m)
	}
	if cmd == nil {
		t.Fatal("unrelated event must continue waiting for the stream")
	}
}

func TestModelListAndStreamFailuresDegradeWithoutDiscardingRows(t *testing.T) {
	closed := 0
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = applyRefresh(t, m, []transport.AgentWithPane{record("agent-1", "codex", "idle", time.Unix(1, 0))})
	m.stream = &transport.EventStream{Close: func() error { closed++; return nil }}

	m = update(t, m, refreshResultMsg{err: errors.New("daemon unavailable")})
	if m.connection != "degraded" || len(m.rows) != 1 || m.rows[0].Agent.ID != "agent-1" {
		t.Fatalf("after list error connection=%q rows=%#v", m.connection, rowIDs(m.rows))
	}
	m = update(t, m, streamClosedMsg{err: errors.New("EOF")})
	if m.connection != "degraded" || len(m.rows) != 1 || closed != 1 || !strings.Contains(m.statusText, "EOF") {
		t.Fatalf("after close connection=%q rows=%#v closed=%d status=%q", m.connection, rowIDs(m.rows), closed, m.statusText)
	}
}

func TestModelDegradedConnectionIsNotMaskedByHealthyHalf(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = applyRefresh(t, m, []transport.AgentWithPane{record("agent-1", "codex", "idle", time.Unix(1, 0))})
	m = update(t, m, streamClosedMsg{err: errors.New("EOF")})
	m = applyRefresh(t, m, []transport.AgentWithPane{record("agent-1", "codex", "idle", time.Unix(1, 0))})
	if m.connection != "degraded" {
		t.Fatalf("successful poll masked dropped stream: connection=%q", m.connection)
	}

	m = update(t, m, refreshResultMsg{err: errors.New("list failed")})
	next, _ := m.Update(streamReadyMsg{stream: &transport.EventStream{Events: make(chan transport.Event), Errors: make(chan error)}})
	m = concreteModel(t, next)
	if m.connection != "degraded" {
		t.Fatalf("connected stream masked failed list: connection=%q", m.connection)
	}
}

func TestModelQuitKeysCloseStreamAndReturnQuit(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'q'}}, {Type: tea.KeyCtrlC}} {
		t.Run(key.String(), func(t *testing.T) {
			closed := 0
			m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
			m.stream = &transport.EventStream{Close: func() error { closed++; return nil }}
			next, cmd := m.Update(key)
			got := concreteModel(t, next)
			if cmd == nil || closed != 1 || !got.quitting {
				t.Fatalf("cmd=%v closed=%d quitting=%t", cmd, closed, got.quitting)
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatalf("quit command returned %T", cmd())
			}
		})
	}
}

func concreteModel(t *testing.T, model tea.Model) Model {
	t.Helper()
	m, ok := model.(Model)
	if !ok {
		t.Fatalf("model type = %T, want Model", model)
	}
	return m
}

func applyRefresh(t *testing.T, m Model, records []transport.AgentWithPane) Model {
	t.Helper()
	m.refreshing = true
	return update(t, m, refreshResultMsg{agents: transport.ListAgentsResponse{Agents: records}, at: time.Unix(1000, 0)})
}

func update(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return concreteModel(t, next)
}

func record(id, kind, state string, created time.Time) transport.AgentWithPane {
	return transport.AgentWithPane{
		Agent: transport.Agent{ID: id, Kind: kind, State: state, CreatedAt: created, StateChangedAt: created},
		Pane:  transport.Pane{ID: "pane-" + id, CWD: "/workspace/" + id},
	}
}

func rowIDs(rows []transport.AgentWithPane) []string {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.Agent.ID
	}
	return ids
}
