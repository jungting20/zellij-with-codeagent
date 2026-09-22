package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSchemaV1MigrationPreservesRecordsAndDetachedFollowups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacyTables := []Table{Sessions, Tabs, Panes, Agents, Metadata}
	for _, table := range legacyTables {
		if _, err = legacy.Exec("CREATE TABLE " + string(table) + " (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL, pane_id TEXT NOT NULL, data TEXT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
		if _, err = legacy.Exec("INSERT INTO "+string(table)+" VALUES (?, ?, ?, ?)", "existing", "parent", "pane", `{"kept":true}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec("CREATE UNIQUE INDEX agents_pane ON agents(pane_id); PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err = w.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("schema version=%d, error=%v", version, err)
	}
	for _, table := range legacyTables {
		rows, err := w.Load(table)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s rows=%+v, error=%v", table, rows, err)
		}
		if row := rows[0]; row.ID != "existing" || row.Parent != "parent" || row.PaneID != "pane" || string(row.Data) != `{"kept":true}` {
			t.Fatalf("migration changed %s row: %+v", table, row)
		}
	}
	if _, err = w.db.Exec(`INSERT INTO agents VALUES ('duplicate', '', 'pane', '{}')`); err == nil {
		t.Fatal("migration lost the unique pane index")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = w.EnqueueAndWait(ctx, Change{Table: Followups, ID: "queue", Parent: "existing", PaneID: "pane", Value: "reserved"}); err != nil {
		t.Fatal(err)
	}
	if err = w.EnqueueAndWait(ctx, Change{Table: Agents, ID: "existing"}); err != nil {
		t.Fatal(err)
	}
	closeWriter(t, w)
	w, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWriter(t, w)
	rows, err := w.Load(Followups)
	if err != nil || len(rows) != 1 || rows[0].Parent != "existing" || rows[0].PaneID != "pane" || string(rows[0].Data) != `"reserved"` {
		t.Fatalf("followup lost after agent deletion/restart: rows=%+v, error=%v", rows, err)
	}
}

func openBlockedWriter(t *testing.T) (*Writer, *sql.DB, <-chan error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	failed := make(chan error, 1)
	w, err := Open(path, func(err error) {
		select {
		case failed <- err:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	// Keep contention tests quick while exercising the production retry path.
	if _, err = w.db.Exec("PRAGMA busy_timeout=25"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	other.SetMaxOpenConns(1)
	if _, err = other.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		other.Exec("ROLLBACK")
		other.Close()
	})
	return w, other, failed
}

func waitPersistenceFailure(t *testing.T, failed <-chan error) {
	t.Helper()
	select {
	case <-failed:
	case <-time.After(3 * time.Second):
		t.Fatal("expected a blocked transaction and retry diagnostic")
	}
}

func waitPersistenceResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("persistence waiter did not finish")
		return nil
	}
}

func TestEnqueueAndWaitAcknowledgesOnlyCommittedTransactions(t *testing.T) {
	w, other, failed := openBlockedWriter(t)
	result := make(chan error, 1)
	go func() {
		result <- w.EnqueueAndWait(context.Background(), Change{Table: Followups, ID: "queue", Value: "sending"})
	}()
	waitPersistenceFailure(t, failed)
	select {
	case err := <-result:
		t.Fatalf("acknowledged before transaction committed: %v", err)
	default:
	}
	var count int
	if err := other.QueryRow("SELECT count(*) FROM followup_queues").Scan(&count); err != nil || count != 0 {
		t.Fatalf("uncommitted rows=%d, error=%v", count, err)
	}
	if _, err := other.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	if err := waitPersistenceResult(t, result); err != nil {
		t.Fatal(err)
	}
	var data string
	if err := other.QueryRow("SELECT data FROM followup_queues WHERE id='queue'").Scan(&data); err != nil || data != `"sending"` {
		t.Fatalf("acknowledged row not visible to another connection: data=%s, error=%v", data, err)
	}
	closeWriter(t, w)
}

func TestEnqueueAndWaitSharesAsyncFIFOAcrossBatches(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "state.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWriter(t, w)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for round := 0; round < 2; round++ {
		for i := 0; i < 600; i++ {
			w.Enqueue(Change{Table: Followups, ID: "queue", Value: i})
		}
		if err = w.EnqueueAndWait(ctx, Change{Table: Followups, ID: "queue", Value: fmt.Sprintf("barrier-%d", round)}); err != nil {
			t.Fatal(err)
		}
		rows, err := w.Load(Followups)
		if err != nil || len(rows) != 1 || string(rows[0].Data) != fmt.Sprintf(`"barrier-%d"`, round) {
			t.Fatalf("async writes overtook durable change: rows=%+v, error=%v", rows, err)
		}
	}
}

func TestEnqueueAndWaitCancellationRetainsAlreadyQueuedChange(t *testing.T) {
	w, other, failed := openBlockedWriter(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- w.EnqueueAndWait(ctx, Change{Table: Followups, ID: "queue", Value: "sending"})
	}()
	waitPersistenceFailure(t, failed)
	cancel()
	if err := waitPersistenceResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error=%v", err)
	}
	if _, err := other.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	barrierCtx, barrierCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer barrierCancel()
	if err := w.EnqueueAndWait(barrierCtx, Change{Table: Metadata, ID: "barrier", Value: true}); err != nil {
		t.Fatal(err)
	}
	rows, err := w.Load(Followups)
	if err != nil || len(rows) != 1 || string(rows[0].Data) != `"sending"` {
		t.Fatalf("canceled waiter lost queued change: rows=%+v, error=%v", rows, err)
	}
	if err = w.EnqueueAndWait(ctx, Change{Table: Followups, ID: "never-queued", Value: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled context error=%v", err)
	}
	closeWriter(t, w)
}

func TestEnqueueAndWaitFailsUncommittedWaitersOnForcedShutdown(t *testing.T) {
	w, _, failed := openBlockedWriter(t)
	first := make(chan error, 1)
	go func() {
		first <- w.EnqueueAndWait(context.Background(), Change{Table: Followups, ID: "in-flight", Value: true})
	}()
	waitPersistenceFailure(t, failed)
	second := make(chan error, 1)
	go func() {
		second <- w.EnqueueAndWait(context.Background(), Change{Table: Followups, ID: "pending", Value: true})
	}()
	// The failed transaction remains in-flight during its retry delay, so the
	// second waiter exercises shutdown acknowledgement of the pending FIFO.
	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		pending := len(w.pending)
		w.mu.Unlock()
		if pending > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second waiter did not enter the pending FIFO")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Close(ctx); err == nil {
		t.Fatal("forced shutdown did not report unsaved changes")
	}
	for _, result := range []<-chan error{first, second} {
		if err := waitPersistenceResult(t, result); !errors.Is(err, ErrClosed) || !errors.Is(err, context.Canceled) {
			t.Fatalf("uncommitted waiter shutdown error=%v", err)
		}
	}
	if err := w.EnqueueAndWait(context.Background(), Change{Table: Followups, ID: "late", Value: true}); !errors.Is(err, ErrClosed) {
		t.Fatalf("enqueue after close error=%v", err)
	}
}
