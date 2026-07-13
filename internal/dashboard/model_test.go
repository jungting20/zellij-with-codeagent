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

func applyRefresh(t *testing.T, m Model, status transport.InspectRuntimeResponse) Model {
	t.Helper()
	m.refreshing = true
	next, _ := m.Update(refreshResultMsg{status: status})
	return next.(Model)
}
