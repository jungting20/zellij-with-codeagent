package registry

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRegisterPaneCreatesStableLogicalRecord(t *testing.T) {
	registry := newTestRegistry()
	tabID := ZellijTabID(3)

	record, err := registry.RegisterPane(RegisterPaneRequest{
		ID:           "pane-1",
		TaskID:       "task-1",
		AgentID:      "agent-1",
		ZellijPaneID: "terminal_5",
		ZellijTabID:  &tabID,
		TabName:      "tests",
		Role:         "test",
		Command:      []string{"go", "test", "./..."},
		CWD:          "/workspace",
	})
	if err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}

	if record.ID != "pane-1" {
		t.Fatalf("record.ID = %q, want pane-1", record.ID)
	}
	if record.Status != PaneStatusStarting {
		t.Fatalf("record.Status = %q, want %q", record.Status, PaneStatusStarting)
	}
	if record.ZellijPaneID != "terminal_5" {
		t.Fatalf("record.ZellijPaneID = %q, want terminal_5", record.ZellijPaneID)
	}
	if record.ZellijTabID == nil || *record.ZellijTabID != 3 || record.TabName != "tests" {
		t.Fatalf("record tab metadata = (%v, %q), want (3, tests)", record.ZellijTabID, record.TabName)
	}
	if !reflect.DeepEqual(record.Command, []string{"go", "test", "./..."}) {
		t.Fatalf("record.Command = %#v, want go test command", record.Command)
	}

	record.Command[0] = "mutated"
	stored, err := registry.GetPane("pane-1")
	if err != nil {
		t.Fatalf("GetPane() error = %v", err)
	}
	if stored.Command[0] != "go" {
		t.Fatalf("stored command was mutated through returned record: %#v", stored.Command)
	}
}

func TestRegisterPaneRejectsDuplicateLogicalID(t *testing.T) {
	registry := newTestRegistry()

	if _, err := registry.RegisterPane(RegisterPaneRequest{ID: "pane-1"}); err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}

	_, err := registry.RegisterPane(RegisterPaneRequest{ID: "pane-1"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RegisterPane() error = %v, want %v", err, ErrAlreadyExists)
	}
}

func TestUpdatePaneStatusPreservesAssociations(t *testing.T) {
	registry := newTestRegistry()
	tabID := ZellijTabID(2)

	if _, err := registry.RegisterPane(RegisterPaneRequest{
		ID:           "pane-1",
		TaskID:       "task-1",
		AgentID:      "agent-1",
		ZellijPaneID: "terminal_5",
		ZellijTabID:  &tabID,
		TabName:      "server",
		Role:         "server",
		Status:       PaneStatusRunning,
	}); err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}

	record, err := registry.UpdatePaneStatus("pane-1", PaneStatusExited, "process exited")
	if err != nil {
		t.Fatalf("UpdatePaneStatus() error = %v", err)
	}

	if record.Status != PaneStatusExited {
		t.Fatalf("record.Status = %q, want %q", record.Status, PaneStatusExited)
	}
	if record.StatusMessage != "process exited" {
		t.Fatalf("record.StatusMessage = %q, want process exited", record.StatusMessage)
	}
	if record.TaskID != "task-1" || record.AgentID != "agent-1" || record.ZellijPaneID != "terminal_5" || record.ZellijTabID == nil || *record.ZellijTabID != 2 || record.TabName != "server" {
		t.Fatalf("record associations changed unexpectedly: %#v", record)
	}
}

func TestUpdatePaneOutput(t *testing.T) {
	registry := newTestRegistry()

	if _, err := registry.RegisterPane(RegisterPaneRequest{ID: "pane-1"}); err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}

	record, err := registry.UpdatePaneOutput("pane-1", "PASS auth_refresh_test")
	if err != nil {
		t.Fatalf("UpdatePaneOutput() error = %v", err)
	}

	if record.LastOutput != "PASS auth_refresh_test" {
		t.Fatalf("record.LastOutput = %q, want output", record.LastOutput)
	}
}

func TestRemoveUnknownPaneReturnsNotFound(t *testing.T) {
	registry := newTestRegistry()

	_, err := registry.RemovePane("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemovePane() error = %v, want %v", err, ErrNotFound)
	}
}

func TestRemovePaneDeletesLogicalRecord(t *testing.T) {
	registry := newTestRegistry()

	if _, err := registry.RegisterPane(RegisterPaneRequest{
		ID:           "pane-1",
		ZellijPaneID: "terminal_5",
	}); err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}

	removed, err := registry.RemovePane("pane-1")
	if err != nil {
		t.Fatalf("RemovePane() error = %v", err)
	}
	if removed.ID != "pane-1" {
		t.Fatalf("removed.ID = %q, want pane-1", removed.ID)
	}

	if _, err := registry.GetPane("pane-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPane() error = %v, want %v", err, ErrNotFound)
	}
	if _, err := registry.GetLatestByZellijPaneID("terminal_5"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLatestByZellijPaneID() error = %v, want %v", err, ErrNotFound)
	}
}

func TestListPanesReturnsStableOrder(t *testing.T) {
	registry := newTestRegistry()

	for _, id := range []PaneID{"pane-b", "pane-a", "pane-c"} {
		if _, err := registry.RegisterPane(RegisterPaneRequest{ID: id}); err != nil {
			t.Fatalf("RegisterPane(%q) error = %v", id, err)
		}
	}

	records := registry.ListPanes()
	got := []PaneID{records[0].ID, records[1].ID, records[2].ID}
	want := []PaneID{"pane-a", "pane-b", "pane-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListPanes() IDs = %#v, want %#v", got, want)
	}
}

func TestReusingZellijPaneIDDoesNotMutateOldLogicalRecord(t *testing.T) {
	registry := newTestRegistry()

	if _, err := registry.RegisterPane(RegisterPaneRequest{
		ID:           "pane-old",
		TaskID:       "task-old",
		AgentID:      "agent-old",
		ZellijPaneID: "terminal_5",
		Status:       PaneStatusClosed,
	}); err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}

	if _, err := registry.RegisterPane(RegisterPaneRequest{
		ID:           "pane-new",
		TaskID:       "task-new",
		AgentID:      "agent-new",
		ZellijPaneID: "terminal_5",
		Status:       PaneStatusRunning,
	}); err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}

	oldRecord, err := registry.GetPane("pane-old")
	if err != nil {
		t.Fatalf("GetPane(old) error = %v", err)
	}
	if oldRecord.TaskID != "task-old" || oldRecord.AgentID != "agent-old" || oldRecord.Status != PaneStatusClosed {
		t.Fatalf("old record was mutated unexpectedly: %#v", oldRecord)
	}

	latest, err := registry.GetLatestByZellijPaneID("terminal_5")
	if err != nil {
		t.Fatalf("GetLatestByZellijPaneID() error = %v", err)
	}
	if latest.ID != "pane-new" {
		t.Fatalf("latest.ID = %q, want pane-new", latest.ID)
	}
}

func newTestRegistry() *Registry {
	return NewWithClock(func() time.Time {
		return time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	})
}
