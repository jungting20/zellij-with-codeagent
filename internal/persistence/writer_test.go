package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func closeWriter(t *testing.T, w *Writer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestOrderedChangesDrainAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		w.Enqueue(Change{Table: Agents, ID: "a", PaneID: "p", Value: i})
	}
	w.Enqueue(Change{Table: Agents, ID: "a"}, Change{Table: Agents, ID: "b", PaneID: "p", Value: "replacement"})
	closeWriter(t, w)
	w, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWriter(t, w)
	rows, err := w.Load(Agents)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "b" || string(rows[0].Data) != `"replacement"` {
		t.Fatalf("rows=%+v", rows)
	}
}
func TestStorageLockDoesNotBlockEnqueueAndRetriesInOrder(t *testing.T) {
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
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer other.Exec("ROLLBACK")
	enqueued := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			w.Enqueue(Change{Table: Metadata, ID: "counter", Value: i})
		}
		close(enqueued)
	}()
	select {
	case <-enqueued:
	case <-time.After(time.Second):
		t.Fatal("enqueue waited on disk")
	}
	select {
	case <-failed:
	case <-time.After(3 * time.Second):
		t.Fatal("expected retry diagnostic")
	}
	if _, err = other.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	closeWriter(t, w)
	w, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWriter(t, w)
	rows, err := w.Load(Metadata)
	if err != nil {
		t.Fatal(err)
	}
	var value int
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	if err = json.Unmarshal(rows[0].Data, &value); err != nil {
		t.Fatal(err)
	}
	if value != 999 {
		t.Fatalf("last change=%d", value)
	}
}
func TestCloseDeadlineReportsUnsavedChangesAndReleasesLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A value that cannot be encoded forces a persistent write failure.
	w.Enqueue(Change{Table: Metadata, ID: "bad", Value: make(chan int)})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err = w.Close(ctx); err == nil {
		t.Fatal("expected drain error")
	}
	reopened, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	closeWriter(t, reopened)
}
func TestExclusiveOwnershipAndSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Open(path, nil); err == nil {
		closeWriter(t, second)
		t.Fatal("second daemon acquired DB")
	}
	if _, err = w.db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	closeWriter(t, w)
	if reopened, err := Open(path, nil); err == nil {
		closeWriter(t, reopened)
		t.Fatal("accepted future schema")
	}
}
func TestResolvePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	path, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "daemon.db" || filepath.Base(filepath.Dir(path)) != "zellij-agent" {
		t.Fatal(path)
	}
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	if path, err = ResolvePath(""); err != nil || path != filepath.Join(root, "zellij-agent", "daemon.db") {
		t.Fatal(path, err)
	}
	explicit := filepath.Join(t.TempDir(), "custom.db")
	if path, err = ResolvePath(explicit); err != nil || path != explicit {
		t.Fatal(path, err)
	}
}

func TestDatabaseSymlinkCannotBypassExclusiveOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWriter(t, w)
	alias := filepath.Join(t.TempDir(), "alias.db")
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if second, err := Open(alias, nil); err == nil {
		closeWriter(t, second)
		t.Fatal("symlink bypassed ownership lock")
	}
}
