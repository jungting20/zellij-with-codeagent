// Package persistence asynchronously commits ordered memory-store changes.
package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

type Table string

const (
	Sessions  Table = "sessions"
	Tabs      Table = "tabs"
	Panes     Table = "panes"
	Agents    Table = "agents"
	Metadata  Table = "metadata"
	Followups Table = "followup_queues"
)

const schemaVersion = 2

var tables = []Table{Sessions, Tabs, Panes, Agents, Metadata, Followups}

// ErrClosed means the writer cannot accept or finish an uncommitted change.
var ErrClosed = errors.New("persistence: writer closed")

// Change owns an immutable copy of Value. A nil Value deletes the row.
// Parent and PaneID expose relationships without decoding the JSON payload.
type Change struct {
	Table              Table
	ID, Parent, PaneID string
	Value              any
}
type Row struct {
	ID, Parent, PaneID string
	Data               json.RawMessage
}

// queuedChange keeps commit acknowledgements out of the persisted payload.
type queuedChange struct {
	Change
	committed chan error
}

type Writer struct {
	db      *sql.DB
	lock    *os.File
	mu      sync.Mutex
	pending []queuedChange
	closing bool
	wake    chan struct{}
	done    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	err     error
	report  func(error)
}

func ResolvePath(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" || !filepath.IsAbs(root) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(root, "zellij-agent", "daemon.db"), nil
}

func Open(path string, report func(error)) (*Writer, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Resolve aliases before locking so a symlink cannot create two owners.
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	} else if !os.IsNotExist(resolveErr) {
		return nil, resolveErr
	} else {
		parent, resolveErr := filepath.EvalSymlinks(filepath.Dir(path))
		if resolveErr != nil {
			return nil, resolveErr
		}
		path = filepath.Join(parent, filepath.Base(path))
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("daemon DB already in use: %w", err)
	}
	release := func() { syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); lock.Close() }
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		release()
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		release()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = initialize(db); err != nil {
		db.Close()
		release()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Writer{db: db, lock: lock, wake: make(chan struct{}, 1), done: make(chan struct{}), ctx: ctx, cancel: cancel, report: report}
	go w.run()
	return w, nil
}

func initialize(db *sql.DB) error {
	for _, stmt := range []string{"PRAGMA busy_timeout=1000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("unsupported daemon DB schema version %d", version)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Creating missing tables upgrades v1 without rewriting its rows. Followup
	// queues deliberately have no foreign keys: removed agents retain their queue.
	for _, table := range tables {
		if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS " + string(table) + " (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL, pane_id TEXT NOT NULL, data TEXT NOT NULL)"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS agents_pane ON agents(pane_id)"); err != nil {
		return err
	}
	if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// Enqueue only holds a short memory lock. It never serializes data or waits on
// disk. The FIFO grows during a storage outage; no changes are silently dropped.
// Call under the producer's state lock so updates cannot overtake one another.
func (w *Writer) Enqueue(changes ...Change) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing {
		panic("persistence: enqueue after producers stopped")
	}
	for _, change := range changes {
		w.pending = append(w.pending, queuedChange{Change: change})
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// EnqueueAndWait joins the same FIFO as Enqueue and returns nil only after its
// transaction commits. The caller must own an immutable copy of change.Value.
// Cancellation stops waiting but does not retract a queued change; its result
// may be unknown to the caller. Callers must not retry such a change blindly.
func (w *Writer) EnqueueAndWait(ctx context.Context, change Change) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	committed := make(chan error, 1)
	w.mu.Lock()
	if w.closing || w.ctx.Err() != nil {
		w.mu.Unlock()
		return ErrClosed
	}
	w.pending = append(w.pending, queuedChange{Change: change, committed: committed})
	select {
	case w.wake <- struct{}{}:
	default:
	}
	w.mu.Unlock()
	select {
	case err := <-committed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func validTable(table Table) bool {
	for _, t := range tables {
		if t == table {
			return true
		}
	}
	return false
}

// Load is used before producers start. Reads never replace the in-memory query path.
func (w *Writer) Load(table Table) ([]Row, error) {
	if !validTable(table) {
		return nil, fmt.Errorf("invalid table %q", table)
	}
	rows, err := w.db.Query("SELECT id,parent_id,pane_id,data FROM " + string(table) + " ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Row
	for rows.Next() {
		var row Row
		var data []byte
		if err = rows.Scan(&row.ID, &row.Parent, &row.PaneID, &data); err != nil {
			return nil, err
		}
		row.Data = json.RawMessage(data)
		result = append(result, row)
	}
	return result, rows.Err()
}

func (w *Writer) commit(batch []queuedChange) error {
	tx, err := w.db.BeginTx(w.ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range batch {
		if !validTable(c.Table) {
			return fmt.Errorf("invalid table %q", c.Table)
		}
		if c.Value == nil {
			_, err = tx.ExecContext(w.ctx, "DELETE FROM "+string(c.Table)+" WHERE id=?", c.ID)
		} else {
			var data []byte
			data, err = json.Marshal(c.Value)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(w.ctx, "INSERT INTO "+string(c.Table)+" (id,parent_id,pane_id,data) VALUES (?,?,?,?) ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id,pane_id=excluded.pane_id,data=excluded.data", c.ID, c.Parent, c.PaneID, string(data))
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (w *Writer) run() {
	defer close(w.done)
	var batch []queuedChange
	defer func() {
		w.mu.Lock()
		w.closing = true
		pending := w.pending
		w.pending = nil
		w.mu.Unlock()
		err := errors.Join(ErrClosed, w.err)
		acknowledge(batch, err)
		acknowledge(pending, err)
	}()
	for {
		if len(batch) == 0 {
			w.mu.Lock()
			n := min(len(w.pending), 256)
			batch = append(batch, w.pending[:n]...)
			clear(w.pending[:n])
			w.pending = w.pending[n:]
			closing := w.closing
			w.mu.Unlock()
			if len(batch) == 0 {
				if closing {
					return
				}
				select {
				case <-w.ctx.Done():
					w.err = w.ctx.Err()
					return
				case <-w.wake:
					continue
				}
			}
		}
		if err := w.commit(batch); err != nil {
			if w.report != nil {
				w.report(fmt.Errorf("SQLite save failed; retaining %d changes for retry: %w", len(batch), err))
			}
			select {
			case <-w.ctx.Done():
				w.err = errors.Join(w.ctx.Err(), err)
				return
			case <-time.After(time.Second):
				continue
			}
		}
		acknowledge(batch, nil)
		clear(batch)
		batch = batch[:0]
	}
}

func acknowledge(batch []queuedChange, err error) {
	for _, change := range batch {
		if change.committed != nil {
			change.committed <- err
		}
	}
}

// Close must follow producer shutdown. It drains the FIFO, or reports unsaved
// changes when ctx expires. The writer is joined before closing its connection.
func (w *Writer) Close(ctx context.Context) error {
	w.mu.Lock()
	w.closing = true
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	select {
	case <-w.done:
	case <-ctx.Done():
		w.cancel()
		<-w.done
	}
	w.cancel()
	err := errors.Join(w.err, w.db.Close())
	syscall.Flock(int(w.lock.Fd()), syscall.LOCK_UN)
	return errors.Join(err, w.lock.Close())
}
