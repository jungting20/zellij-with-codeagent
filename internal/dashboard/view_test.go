package dashboard

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/transport"
)

func TestViewHandlesEmptyAndTinyWindows(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	for _, size := range []tea.WindowSizeMsg{{Width: 0, Height: 0}, {Width: 20, Height: 4}, {Width: 100, Height: 30}} {
		next, _ := m.Update(size)
		m = next.(Model)
		view := m.View()
		if !strings.Contains(view, "RUNTIME DASHBOARD") || !strings.Contains(view, "no managed panes") {
			t.Fatalf("size=%#v view=%q", size, view)
		}
	}
}

func TestViewShowsLifecycleBadgesAndFiltersRawOutput(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m.width, m.height = 100, 30
	m.refreshing = true
	next, _ := m.Update(refreshResultMsg{
		status: transport.InspectRuntimeResponse{Panes: []transport.Pane{{ID: "coder", SessionID: "s", TaskID: "t", TabID: "tab", Role: "coding-agent", Status: "running"}}},
		events: transport.RecentEventsResponse{Events: []transport.Event{
			{Type: "raw_output", Message: "hidden-raw"},
			{Type: "test_passed", PaneID: "coder", Message: "visible-event"},
		}},
	})
	m = next.(Model)
	view := m.View()
	for _, want := range []string{"coder", "role=coding-agent", "[running]", "test_passed", "visible-event"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "hidden-raw") {
		t.Fatalf("view contains raw output event: %q", view)
	}
}
