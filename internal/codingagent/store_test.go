package codingagent_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/runtime"
)

func TestMemoryStoreCreateGetReturnsCopies(t *testing.T) {
	store := codingagent.NewMemoryStore(func() time.Time { return time.Unix(20, 0) })
	want := testRecord("agent-1", "pane-1", time.Unix(10, 0))

	created, err := store.Create(want)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(created, want) {
		t.Fatalf("Create() = %#v, want %#v", created, want)
	}

	got, err := store.Get(want.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got.StateReason = "mutated returned value"

	again, err := store.Get(want.ID)
	if err != nil {
		t.Fatalf("Get() second error = %v", err)
	}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("Get() retained mutation = %#v, want %#v", again, want)
	}
}

func TestMemoryStoreRejectsDuplicateIDAndPane(t *testing.T) {
	store := codingagent.NewMemoryStore(time.Now)
	first := testRecord("agent-1", "pane-1", time.Unix(10, 0))
	if _, err := store.Create(first); err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}

	duplicateID := testRecord("agent-1", "pane-2", time.Unix(11, 0))
	if _, err := store.Create(duplicateID); !errors.Is(err, codingagent.ErrDuplicateID) {
		t.Fatalf("Create(duplicate ID) error = %v, want ErrDuplicateID", err)
	}

	duplicatePane := testRecord("agent-2", "pane-1", time.Unix(11, 0))
	if _, err := store.Create(duplicatePane); !errors.Is(err, codingagent.ErrDuplicatePane) {
		t.Fatalf("Create(duplicate pane) error = %v, want ErrDuplicatePane", err)
	}
}

func TestMemoryStoreListSortsByCreatedAtThenID(t *testing.T) {
	store := codingagent.NewMemoryStore(time.Now)
	for _, record := range []codingagent.Record{
		testRecord("agent-b", "pane-b", time.Unix(11, 0)),
		testRecord("agent-c", "pane-c", time.Unix(10, 0)),
		testRecord("agent-a", "pane-a", time.Unix(10, 0)),
	} {
		if _, err := store.Create(record); err != nil {
			t.Fatalf("Create(%q) error = %v", record.ID, err)
		}
	}

	got, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	wantIDs := []codingagent.ID{"agent-a", "agent-c", "agent-b"}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Fatalf("List()[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
	got[0].StateReason = "mutated returned value"
	again, err := store.Get("agent-a")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if again.StateReason != "" {
		t.Fatalf("Get() retained list mutation: %#v", again)
	}
}

func TestMemoryStoreGetByPaneDeleteAndNotFound(t *testing.T) {
	store := codingagent.NewMemoryStore(time.Now)
	record := testRecord("agent-1", "pane-1", time.Unix(10, 0))
	if _, err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.GetByPane(record.PaneID)
	if err != nil {
		t.Fatalf("GetByPane() error = %v", err)
	}
	if !reflect.DeepEqual(got, record) {
		t.Fatalf("GetByPane() = %#v, want %#v", got, record)
	}
	if err := store.Delete(record.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get(record.ID); !errors.Is(err, codingagent.ErrNotFound) {
		t.Fatalf("Get(deleted) error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetByPane(record.PaneID); !errors.Is(err, codingagent.ErrNotFound) {
		t.Fatalf("GetByPane(deleted) error = %v, want ErrNotFound", err)
	}
	if err := store.Delete(record.ID); !errors.Is(err, codingagent.ErrNotFound) {
		t.Fatalf("Delete(missing) error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreUpdateStateIsAtomicAndOnlyStampsChanges(t *testing.T) {
	clock := time.Unix(20, 0)
	store := codingagent.NewMemoryStore(func() time.Time { return clock })
	record := testRecord("agent-1", "pane-1", time.Unix(10, 0))
	if _, err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	unchanged, err := store.UpdateState(record.ID, codingagent.StateUpdate{State: codingagent.StateUnknown})
	if err != nil {
		t.Fatalf("UpdateState(unchanged) error = %v", err)
	}
	if unchanged.Changed {
		t.Fatal("UpdateState(unchanged).Changed = true, want false")
	}
	if !unchanged.Current.StateChangedAt.Equal(record.StateChangedAt) {
		t.Fatalf("unchanged StateChangedAt = %v, want %v", unchanged.Current.StateChangedAt, record.StateChangedAt)
	}

	clock = time.Unix(30, 0)
	changed, err := store.UpdateState(record.ID, codingagent.StateUpdate{
		State:       codingagent.StateWorking,
		Reason:      "matched busy prompt",
		MatchedRule: "working.prompt",
	})
	if err != nil {
		t.Fatalf("UpdateState(changed) error = %v", err)
	}
	if !changed.Changed {
		t.Fatal("UpdateState(changed).Changed = false, want true")
	}
	if !reflect.DeepEqual(changed.Previous, record) {
		t.Fatalf("UpdateState().Previous = %#v, want %#v", changed.Previous, record)
	}
	if changed.Current.State != codingagent.StateWorking || changed.Current.StateReason != "matched busy prompt" || changed.Current.MatchedRule != "working.prompt" {
		t.Fatalf("UpdateState().Current = %#v, want working state update", changed.Current)
	}
	if !changed.Current.StateChangedAt.Equal(clock) {
		t.Fatalf("changed StateChangedAt = %v, want %v", changed.Current.StateChangedAt, clock)
	}
}

func TestMemoryStoreValidatesRecordsAndUpdates(t *testing.T) {
	store := codingagent.NewMemoryStore(time.Now)
	invalid := testRecord("", "pane-1", time.Unix(10, 0))
	if _, err := store.Create(invalid); !errors.Is(err, codingagent.ErrInvalidRecord) {
		t.Fatalf("Create(empty ID) error = %v, want ErrInvalidRecord", err)
	}

	record := testRecord("agent-1", "pane-1", time.Unix(10, 0))
	if _, err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.UpdateState(record.ID, codingagent.StateUpdate{State: "invalid"}); !errors.Is(err, codingagent.ErrInvalidState) {
		t.Fatalf("UpdateState(invalid) error = %v, want ErrInvalidState", err)
	}
}

func TestMemoryStoreAcceptsCanonicalAccessModes(t *testing.T) {
	store := codingagent.NewMemoryStore(time.Now)
	for _, mode := range []codingagent.AccessMode{"", codingagent.AccessFull, codingagent.AccessReadOnly} {
		record := testRecord(codingagent.ID("agent-"+string(mode)), runtime.PaneID("pane-"+string(mode)), time.Unix(10, 0))
		record.AccessMode = mode
		if _, err := store.Create(record); err != nil {
			t.Fatalf("Create(access %q) error = %v", mode, err)
		}
	}
}

func TestMemoryStoreRejectsUnknownAccessMode(t *testing.T) {
	store := codingagent.NewMemoryStore(time.Now)
	record := testRecord("agent-1", "pane-1", time.Unix(10, 0))
	record.AccessMode = "limited"
	_, err := store.Create(record)
	if !errors.Is(err, codingagent.ErrInvalidRecord) || !errors.Is(err, codingagent.ErrInvalidAccessMode) {
		t.Fatalf("Create(unknown access) error = %v, want ErrInvalidRecord and ErrInvalidAccessMode", err)
	}
}

func testRecord(id codingagent.ID, paneID runtime.PaneID, createdAt time.Time) codingagent.Record {
	return codingagent.Record{
		ID:             id,
		Kind:           codingagent.KindCodex,
		PaneID:         paneID,
		State:          codingagent.StateUnknown,
		CreatedAt:      createdAt,
		StateChangedAt: createdAt,
	}
}
