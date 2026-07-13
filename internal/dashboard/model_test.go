package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/transport"
)

type fakeClient struct {
	status           transport.InspectRuntimeResponse
	events           transport.RecentEventsResponse
	snapshot         transport.SnapshotOutputResponse
	stream           *transport.EventStream
	inspectErr       error
	eventsErr        error
	snapshotErr      error
	streamErr        error
	snapshotPane     string
	snapshotRequests int
	inputPane        string
	input            transport.SendInputRequest
	inputErr         error
	reconcile        transport.ReconcileResponse
	reconcileErr     error
	cleanup          transport.CleanupRequest
	cleanupResponse  transport.CleanupResponse
	cleanupErr       error
}

func (f *fakeClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	return f.status, f.inspectErr
}

func (f *fakeClient) RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error) {
	return f.events, f.eventsErr
}

func (f *fakeClient) StreamEvents(context.Context) (*transport.EventStream, error) {
	return f.stream, f.streamErr
}

func (f *fakeClient) SnapshotOutput(_ context.Context, paneID string, _ transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	f.snapshotPane = paneID
	f.snapshotRequests++
	return f.snapshot, f.snapshotErr
}

func (f *fakeClient) SendInput(_ context.Context, paneID string, req transport.SendInputRequest) error {
	f.inputPane = paneID
	f.input = req
	return f.inputErr
}

func (f *fakeClient) Reconcile(context.Context) (transport.ReconcileResponse, error) {
	return f.reconcile, f.reconcileErr
}

func (f *fakeClient) Cleanup(_ context.Context, req transport.CleanupRequest) (transport.CleanupResponse, error) {
	f.cleanup = req
	return f.cleanupResponse, f.cleanupErr
}

func TestModelCoalescesRefreshTriggers(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{RefreshInterval: time.Second, EventLimit: 5})
	m.refreshing = true
	next, _ := m.Update(refreshTickMsg{})
	next, _ = next.(Model).Update(streamEventMsg{event: transport.Event{Type: "raw_output"}})
	got := next.(Model)
	if !got.refreshDirty || !got.refreshing {
		t.Fatalf("model = %#v, want one in-flight refresh marked dirty", got)
	}
}

func TestModelStreamCloseKeepsPollingDegraded(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{RefreshInterval: time.Second, EventLimit: 5})
	next, cmd := m.Update(streamClosedMsg{err: errors.New("eof")})
	got := next.(Model)
	if got.connection != "degraded" || !strings.Contains(got.statusText, "eof") {
		t.Fatalf("model = %#v, want degraded eof", got)
	}
	if cmd != nil {
		t.Fatal("stream close must not quit")
	}
}

func TestModelPreservesSelectionAcrossRefresh(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	first := transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
		{ID: "b", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
	}}
	m = applyRefresh(t, m, first)
	for i, row := range m.rows {
		if row.node.pane != nil && row.node.pane.ID == "b" {
			m.selected = i
			m.selectedKey = row.node.key
		}
	}
	second := transport.InspectRuntimeResponse{Panes: []transport.Pane{first.Panes[1], first.Panes[0]}}
	m = applyRefresh(t, m, second)
	if pane := m.selectedPane(); pane == nil || pane.ID != "b" {
		t.Fatalf("selected pane = %#v, want b", pane)
	}
}

func TestModelFallsBackWhenSelectedPaneDisappears(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
		{ID: "b", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
	}})
	m.selected = len(m.rows) - 1
	m.selectedKey = m.rows[m.selected].node.key
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
	}})
	if m.selected < 0 || m.selected >= len(m.rows) {
		t.Fatalf("selected = %d rows=%d", m.selected, len(m.rows))
	}
}

func TestModelSelectionRequestsSnapshot(t *testing.T) {
	client := &fakeClient{snapshot: transport.SnapshotOutputResponse{Output: "hello"}}
	m := NewModel(context.Background(), client, Options{})
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
	}})
	for i, row := range m.rows {
		if row.node.pane != nil {
			m.selected = i - 1
			m.selectedKey = m.rows[m.selected].node.key
			break
		}
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("moving onto a pane must request snapshot")
	}
	msg := cmd()
	next, _ = next.(Model).Update(msg)
	got := next.(Model)
	if client.snapshotPane != "a" || got.snapshots["a"] != "hello" {
		t.Fatalf("snapshot pane=%q output=%q", client.snapshotPane, got.snapshots["a"])
	}
}

func TestModelQueuesSnapshotWhenSelectionChangesDuringSnapshot(t *testing.T) {
	client := &fakeClient{snapshot: transport.SnapshotOutputResponse{Output: "output"}}
	m := NewModel(context.Background(), client, Options{})
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
		{ID: "b", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
	}})
	for i, row := range m.rows {
		if row.node.pane != nil && row.node.pane.ID == "a" {
			m.selected, m.selectedKey = i, row.node.key
		}
	}
	first := m.requestSnapshot("a")
	if first == nil {
		t.Fatal("first snapshot command is nil")
	}
	next, cmd := m.moveSelection(1)
	m = next.(Model)
	if pane := m.selectedPane(); pane == nil || pane.ID != "b" {
		t.Fatalf("selected pane = %#v, want b", pane)
	}
	if cmd != nil {
		t.Fatal("second snapshot must wait for the in-flight snapshot")
	}
	next, followup := m.Update(first())
	if followup == nil {
		t.Fatal("completed snapshot must start queued selected-pane snapshot")
	}
	next, _ = next.(Model).Update(followup())
	if client.snapshotPane != "b" {
		t.Fatalf("last snapshot pane = %q, want b", client.snapshotPane)
	}
}

func applyRefresh(t *testing.T, m Model, status transport.InspectRuntimeResponse) Model {
	t.Helper()
	m.refreshing = true
	next, _ := m.Update(refreshResultMsg{status: status})
	return next.(Model)
}

func TestModelInputSendsLineWithNewline(t *testing.T) {
	client, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("echo ok")})
	next, cmd := next.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter must submit non-empty input")
	}
	msg := cmd()
	next, _ = next.(Model).Update(msg)
	if client.inputPane != "coder" || client.input.Text != "echo ok\n" {
		t.Fatalf("input pane=%q req=%#v", client.inputPane, client.input)
	}
	if next.(Model).mode != "normal" {
		t.Fatalf("mode = %q, want normal", next.(Model).mode)
	}
}

func TestModelInputEditingCancelAndInactiveRejection(t *testing.T) {
	_, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ab")})
	next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := string(next.(Model).input); got != "a" {
		t.Fatalf("input = %q, want a", got)
	}
	next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(Model).mode != "normal" || len(next.(Model).input) != 0 {
		t.Fatalf("cancelled model = %#v", next.(Model))
	}

	_, inactive := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "exited"})
	next, cmd := inactive.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil || next.(Model).mode != "normal" || !strings.Contains(next.(Model).statusText, "inactive") {
		t.Fatalf("inactive input model = %#v cmd=%v", next.(Model), cmd)
	}
}

func TestModelInputKeepsOriginalPaneAcrossRefresh(t *testing.T) {
	client := &fakeClient{}
	m := NewModel(context.Background(), client, Options{})
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
		{ID: "b", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
	}})
	for i, row := range m.rows {
		if row.node.pane != nil && row.node.pane.ID == "a" {
			m.selected, m.selectedKey = i, row.node.key
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("echo")})
	m = next.(Model)
	m.refreshing = true
	next, _ = m.Update(refreshResultMsg{status: transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "b", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
	}}})
	next, cmd := next.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("input command is nil")
	}
	_ = cmd()
	if client.inputPane != "a" {
		t.Fatalf("input pane = %q, want original pane a", client.inputPane)
	}
}

func TestModelEmptyInputDoesNotSubmit(t *testing.T) {
	client, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "starting"})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	next, cmd := next.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || client.inputPane != "" || next.(Model).mode != "input" {
		t.Fatalf("empty submit model=%#v pane=%q cmd=%v", next.(Model), client.inputPane, cmd)
	}
}

func TestModelCleanupRequiresConfirmationAndScopesTask(t *testing.T) {
	client, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || next.(Model).mode != "confirm-cleanup" {
		t.Fatal("x must only enter confirmation")
	}
	next, cmd = next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y must submit cleanup")
	}
	msg := cmd()
	next, _ = next.(Model).Update(msg)
	if client.cleanup.TaskID != "task-1" || len(client.cleanup.PaneIDs) != 0 || client.cleanup.Role != "" {
		t.Fatalf("cleanup = %#v", client.cleanup)
	}
}

func TestModelCleanupCancellationAndMissingTask(t *testing.T) {
	client, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	next, cmd := next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil || client.cleanup.TaskID != "" || next.(Model).mode != "normal" {
		t.Fatalf("cancel cleanup model=%#v request=%#v", next.(Model), client.cleanup)
	}

	_, noTask := modelWithSelectedPane(t, transport.Pane{ID: "coder", Status: "running"})
	next, cmd = noTask.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || next.(Model).mode != "normal" || !strings.Contains(next.(Model).statusText, "task") {
		t.Fatalf("missing task model=%#v cmd=%v", next.(Model), cmd)
	}
}

func TestModelReconcileSummaryAndActionError(t *testing.T) {
	client, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	client.reconcile = transport.ReconcileResponse{Active: []transport.Pane{{ID: "coder"}}, Lost: []transport.Pane{{ID: "lost"}}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("r must reconcile")
	}
	next, _ = next.(Model).Update(cmd())
	if !strings.Contains(next.(Model).statusText, "active=1 lost=1") {
		t.Fatalf("status = %q", next.(Model).statusText)
	}

	client, m = modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	client.inputErr = errors.New("denied")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	next, cmd = next.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(Model).Update(cmd())
	if !strings.Contains(next.(Model).statusText, "input failed: denied") {
		t.Fatalf("status = %q", next.(Model).statusText)
	}
}

func TestModelPreservesActionSummaryAcrossImmediateRefresh(t *testing.T) {
	client, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	client.reconcile = transport.ReconcileResponse{Active: []transport.Pane{{ID: "coder"}}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	next, _ = next.(Model).Update(cmd())
	m = next.(Model)
	m.refreshing = true
	next, _ = m.Update(refreshResultMsg{status: transport.InspectRuntimeResponse{Panes: []transport.Pane{{
		ID: "coder", SessionID: "session", TaskID: "task-1", TabID: "tab", Status: "running",
	}}}})
	if view := next.(Model).View(); !strings.Contains(view, "reconciled active=1 lost=0") {
		t.Fatalf("view lost action summary after refresh: %q", view)
	}
}

func TestModelSuppressesDuplicateAction(t *testing.T) {
	_, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	m.actionInFlight = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil || !next.(Model).actionInFlight {
		t.Fatalf("duplicate action model=%#v cmd=%v", next.(Model), cmd)
	}
}

func TestModelFocusControlsNavigationAndDetailTab(t *testing.T) {
	_, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", Status: "running"})
	selected := m.selected
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.focus != focusDetail || m.selected != selected {
		t.Fatalf("tab model = %#v", m)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.detailTab != tabEvents {
		t.Fatalf("detail tab = %q, want events", m.detailTab)
	}
	m.events = []transport.Event{{Type: "one"}, {Type: "two"}, {Type: "three"}}
	m.height = 8
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if next.(Model).eventViewport.followBottom {
		t.Fatal("scrolling up must leave follow-bottom mode")
	}
}

func TestModelTreeKeysDoNotMoveSelectionWhenDetailFocused(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
		{ID: "b", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"},
	}})
	before := m.selected
	m.focus = focusDetail
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if next.(Model).selected != before {
		t.Fatalf("selection moved from %d to %d", before, next.(Model).selected)
	}
}

func TestModelTreeSelectionStaysInsideViewport(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m.height = 9
	var panes []transport.Pane
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		panes = append(panes, transport.Pane{ID: id, SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"})
	}
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: panes})
	for i := 0; i < len(m.rows); i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
	}
	height := m.panelBodyHeight()
	if m.selected < m.treeViewport.offset || m.selected >= m.treeViewport.offset+height {
		t.Fatalf("selected=%d viewport=%#v height=%d", m.selected, m.treeViewport, height)
	}
}

func TestModelRefreshPreservesPresentationState(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m.focus, m.detailTab = focusDetail, tabEvents
	m.eventViewport = viewport{offset: 1, followBottom: false}
	m.refreshing = true
	at := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	next, _ := m.Update(refreshResultMsg{
		status: transport.InspectRuntimeResponse{Panes: []transport.Pane{{ID: "a", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"}}},
		events: transport.RecentEventsResponse{Events: []transport.Event{{Type: "one"}, {Type: "two"}, {Type: "three"}}},
		at:     at,
	})
	got := next.(Model)
	if got.focus != focusDetail || got.detailTab != tabEvents || got.lastRefresh != at || !got.loaded {
		t.Fatalf("presentation state = %#v", got)
	}
}

func modelWithSelectedPane(t *testing.T, pane transport.Pane) (*fakeClient, Model) {
	t.Helper()
	client := &fakeClient{}
	pane.SessionID = "session"
	if pane.TabID == "" {
		pane.TabID = "tab"
	}
	m := NewModel(context.Background(), client, Options{})
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{pane}})
	for i, row := range m.rows {
		if row.node.pane != nil {
			m.selected = i
			m.selectedKey = row.node.key
			break
		}
	}
	return client, m
}
