package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/transport"
)

func TestViewHandlesEmptyAndTinyWindows(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m.loaded = true
	for _, size := range []tea.WindowSizeMsg{{Width: 0, Height: 0}, {Width: 20, Height: 4}, {Width: 100, Height: 30}} {
		next, _ := m.Update(size)
		m = next.(Model)
		view := m.View()
		if !strings.Contains(view, "RUNTIME DASHBOARD") || !strings.Contains(view, "No managed panes") {
			t.Fatalf("size=%#v view=%q", size, view)
		}
	}
}

func TestViewFitsConfiguredWindow(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m.width, m.height = 30, 6
	m.refreshing = true
	var panes []transport.Pane
	for _, id := range []string{"a", "b", "c", "d"} {
		panes = append(panes, transport.Pane{ID: id, SessionID: "session", TaskID: "task", TabID: "tab", Role: "coding-agent", Status: "running"})
	}
	next, _ := m.Update(refreshResultMsg{status: transport.InspectRuntimeResponse{Panes: panes}})
	view := next.(Model).View()
	lines := strings.Split(view, "\n")
	if len(lines) > m.height {
		t.Fatalf("view lines = %d, want <= %d: %q", len(lines), m.height, view)
	}
	for i, line := range lines {
		if width := ansi.StringWidth(line); width > m.width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, width, m.width, line)
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
	m.detailTab = tabEvents
	for i, row := range m.rows {
		if row.node.pane != nil {
			m.selected = i
			m.selectedKey = row.node.key
			break
		}
	}
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

func TestViewShowsHealthSummaryFocusTabsAndSymbols(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m.width, m.height, m.connection, m.loaded = 120, 24, "live", true
	m.refreshing = true
	next, _ := m.Update(refreshResultMsg{
		status: transport.InspectRuntimeResponse{Panes: []transport.Pane{
			{ID: "coder", SessionID: "s", TaskID: "t", TabID: "tab", Role: "coding-agent", Status: "running"},
			{ID: "tester", SessionID: "s", TaskID: "t", TabID: "tab", Role: "tester", Status: "error"},
		}},
		at: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
	})
	m = next.(Model)
	view := m.View()
	for _, want := range []string{"LIVE", "2 panes", "active=1", "problem=1", "RUNTIME [FOCUSED]", "[Output]", "Events", "* coder", "! tester"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %q", want, view)
		}
	}
}

func TestViewNarrowLayoutShowsOnlyFocusedPanel(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m.width, m.height, m.loaded = 60, 18, true
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{{ID: "coder", SessionID: "s", TaskID: "t", TabID: "tab", Status: "running"}}})
	treeView := m.View()
	if !strings.Contains(treeView, "RUNTIME [FOCUSED]") || strings.Contains(treeView, "DETAIL [FOCUSED]") {
		t.Fatalf("tree-focused narrow view = %q", treeView)
	}
	m.focus = focusDetail
	detailView := m.View()
	if !strings.Contains(detailView, "DETAIL [FOCUSED]") || strings.Contains(detailView, "RUNTIME [FOCUSED]") {
		t.Fatalf("detail-focused narrow view = %q", detailView)
	}
}

func TestViewDistinguishesLoadingFromEmptyRuntime(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	m.width, m.height = 80, 16
	if view := m.View(); !strings.Contains(view, "Loading runtime") {
		t.Fatalf("loading view = %q", view)
	}
	m.loaded = true
	if view := m.View(); !strings.Contains(view, "No managed panes") || !strings.Contains(view, "Start a managed workspace") {
		t.Fatalf("empty view = %q", view)
	}
}

func TestViewRendersInputCleanupAndHelpOverlays(t *testing.T) {
	_, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	m.width, m.height = 100, 24
	cases := []struct {
		mode string
		want []string
	}{
		{mode: "input", want: []string{"INPUT -> coder", "Enter send", "Esc cancel"}},
		{mode: "confirm-cleanup", want: []string{"CLEAN UP TASK task-1", "1 managed pane", "Other tasks and unmanaged panes are not touched"}},
		{mode: "help", want: []string{"DASHBOARD HELP", "Tab focus panels", "r reconcile"}},
	}
	for _, tc := range cases {
		m.mode = tc.mode
		m.inputPane = "coder"
		m.confirmTask = "task-1"
		view := m.View()
		for _, want := range tc.want {
			if !strings.Contains(view, want) {
				t.Fatalf("mode %s view missing %q: %q", tc.mode, want, view)
			}
		}
	}
}

func TestViewReadOnlyTaskFilterAndCapacity(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{TaskID: "tickets-1", ReadOnly: true, Capacity: 4})
	m.width, m.height, m.loaded = 120, 24, true
	m = applyRefresh(t, m, transport.InspectRuntimeResponse{Panes: []transport.Pane{
		{ID: "worker", SessionID: "s", TaskID: "tickets-1", TabID: "tab", Status: "running"},
	}})
	view := m.View()
	for _, want := range []string{"READ ONLY", "task=tickets-1", "active=1/4"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %q", want, view)
		}
	}
	for _, unwanted := range []string{"i input", "r reconcile", "x cleanup"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("read-only view contains mutation key %q: %q", unwanted, view)
		}
	}

	m.mode = "help"
	help := m.View()
	for _, want := range []string{"s snapshot", "R refresh", "? / q / Esc close"} {
		if !strings.Contains(help, want) {
			t.Fatalf("read-only help missing %q: %q", want, help)
		}
	}
	for _, unwanted := range []string{"i input", "r reconcile", "x cleanup"} {
		if strings.Contains(help, unwanted) {
			t.Fatalf("read-only help contains mutation key %q: %q", unwanted, help)
		}
	}
}
