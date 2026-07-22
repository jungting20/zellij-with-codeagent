package ticketworker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const currentSchemaVersion = 4

const schema = `
CREATE TABLE IF NOT EXISTS tickets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    summary TEXT NOT NULL CHECK (length(trim(summary)) > 0),
    spec_path TEXT NOT NULL,
    plan_path TEXT NOT NULL,
    worktree_branch TEXT NOT NULL CHECK (length(trim(worktree_branch)) > 0),
    prompt TEXT NOT NULL CHECK (length(trim(prompt)) > 0),
    status TEXT NOT NULL CHECK (status IN ('ready','in_progress','done','cancelled')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    cancelled_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_tickets_status_fifo
ON tickets(status, created_at, id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tickets_plan_path_unique
ON tickets(plan_path) WHERE plan_path <> '';
PRAGMA user_version = 4;
`

const migrateSchemaV2ToV3 = `
BEGIN;
ALTER TABLE tickets RENAME TO tickets_v2;
CREATE TABLE tickets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    summary TEXT NOT NULL CHECK (length(trim(summary)) > 0),
    spec_path TEXT NOT NULL,
    plan_path TEXT NOT NULL,
    prompt TEXT NOT NULL CHECK (length(trim(prompt)) > 0),
    status TEXT NOT NULL CHECK (status IN ('ready','in_progress','done','cancelled')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    cancelled_at TEXT
);
INSERT INTO tickets(id, title, summary, spec_path, plan_path, prompt, status, created_at, updated_at, started_at, completed_at, cancelled_at)
SELECT id, title, summary, spec_path, plan_path, prompt, status, created_at, updated_at, started_at, completed_at, cancelled_at
FROM tickets_v2;
DROP TABLE tickets_v2;
CREATE INDEX idx_tickets_status_fifo ON tickets(status, created_at, id);
CREATE UNIQUE INDEX idx_tickets_plan_path_unique ON tickets(plan_path) WHERE plan_path <> '';
PRAGMA user_version = 3;
COMMIT;
`

const migrateSchemaV3ToV4 = `
BEGIN;
ALTER TABLE tickets ADD COLUMN worktree_branch TEXT NOT NULL DEFAULT '';
PRAGMA user_version = 4;
COMMIT;
`

type Store struct {
	db   *sql.DB
	root string
	now  func() time.Time
}

// Open preserves the imported ra-ticket Store API. Ticket-worker commands use
// InitializeProject or OpenExisting to enforce explicit project initialization.
func Open(ctx context.Context, root, databasePath string, now func() time.Time) (*Store, error) {
	return openStore(ctx, root, databasePath, now, true)
}

func openStore(ctx context.Context, root, databasePath string, now func() time.Time, create bool) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve ticket repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve ticket repository root: %w", err)
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
			return nil, fmt.Errorf("create ticket database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open ticket database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure ticket database: %w", err)
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read schema version: %w", err)
	}
	if version == 1 {
		_ = db.Close()
		return nil, fmt.Errorf("ticket schema version 1 is unsupported; remove %s and run zellij-agent ticket-worker init", databasePath)
	}
	if version > currentSchemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("unsupported ticket schema version %d", version)
	}
	if version == 2 {
		if _, err := db.ExecContext(ctx, migrateSchemaV2ToV3); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate ticket schema from version 2 to 3: %w", err)
		}
		version = 3
	}
	if version == 3 {
		if _, err := db.ExecContext(ctx, migrateSchemaV3ToV4); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate ticket schema from version 3 to 4: %w", err)
		}
		version = 4
	}
	if version == 0 {
		if _, err := db.ExecContext(ctx, schema); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize ticket schema: %w", err)
		}
	}
	return &Store{db: db, root: root, now: now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Add(ctx context.Context, input CreateInput) (Ticket, error) {
	title, summary, branch, prompt, err := normalizeCreateInput(input)
	if err != nil {
		return Ticket{}, err
	}
	spec, err := s.artifactPath(input.SpecPath, filepath.Join("docs", "superpowers", "specs"))
	if err != nil {
		return Ticket{}, ErrInvalidArtifact
	}
	plan, err := s.artifactPath(input.PlanPath, filepath.Join("docs", "superpowers", "plans"))
	if err != nil {
		return Ticket{}, ErrInvalidArtifact
	}
	return s.insert(ctx, title, summary, spec, plan, branch, prompt)
}

func (s *Store) FastAdd(ctx context.Context, input CreateInput) (Ticket, error) {
	title, summary, branch, prompt, err := normalizeCreateInput(input)
	if err != nil {
		return Ticket{}, err
	}
	return s.insert(ctx, title, summary, "", "", branch, prompt)
}

func normalizeCreateInput(input CreateInput) (string, string, string, string, error) {
	title := strings.TrimSpace(input.Title)
	summary := strings.TrimSpace(input.Summary)
	branch := strings.TrimSpace(input.WorktreeBranch)
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" || strings.Contains(prompt, completionMarkerPrefix) {
		return "", "", "", "", ErrInvalidPrompt
	}
	if title == "" || summary == "" {
		return "", "", "", "", ErrInvalidArtifact
	}
	if branch == "" {
		return "", "", "", "", ErrInvalidWorktreeBranch
	}
	return title, summary, branch, prompt, nil
}

func (s *Store) insert(ctx context.Context, title, summary, spec, plan, branch, prompt string) (Ticket, error) {
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
	INSERT INTO tickets(title, summary, spec_path, plan_path, worktree_branch, prompt, status, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, 'ready', ?, ?)`, title, summary, spec, plan, branch, prompt, stamp, stamp)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: tickets.plan_path") {
			return Ticket{}, ErrDuplicatePlan
		}
		return Ticket{}, fmt.Errorf("insert ticket: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Ticket{}, fmt.Errorf("read ticket id: %w", err)
	}
	return Ticket{ID: id, Title: title, Summary: summary, SpecPath: spec, PlanPath: plan, WorktreeBranch: branch, Prompt: prompt, Status: StatusReady, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) Get(ctx context.Context, id int64) (Ticket, error) {
	ticket, err := scanTicket(s.db.QueryRowContext(ctx, `
	SELECT id, title, summary, spec_path, plan_path, worktree_branch, prompt, status,
       created_at, updated_at, started_at, completed_at, cancelled_at
FROM tickets
WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, fmt.Errorf("get ticket: %w", err)
	}
	return ticket, nil
}

func (s *Store) List(ctx context.Context, filter *Status) ([]Ticket, error) {
	if filter != nil && !validStatus(*filter) {
		return nil, ErrInvalidStatus
	}
	query := `
	SELECT id, title, summary, spec_path, plan_path, worktree_branch, prompt, status,
       created_at, updated_at, started_at, completed_at, cancelled_at
FROM tickets`
	args := []any(nil)
	if filter != nil {
		query += "\nWHERE status = ?"
		args = append(args, *filter)
	}
	query += "\nORDER BY created_at ASC, id ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()

	tickets := make([]Ticket, 0)
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, fmt.Errorf("scan listed ticket: %w", err)
		}
		tickets = append(tickets, ticket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tickets: %w", err)
	}
	return tickets, nil
}

func (s *Store) Next(ctx context.Context) (Ticket, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("acquire next ticket connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Ticket{}, fmt.Errorf("begin next ticket claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	ticket, err := scanTicket(conn.QueryRowContext(ctx, `
	SELECT id, title, summary, spec_path, plan_path, worktree_branch, prompt, status,
       created_at, updated_at, started_at, completed_at, cancelled_at
FROM tickets
WHERE status = 'ready'
ORDER BY created_at ASC, id ASC
LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrEmptyQueue
	}
	if err != nil {
		return Ticket{}, fmt.Errorf("get next ticket: %w", err)
	}

	now := s.now().UTC()
	ticket.Status = StatusInProgress
	ticket.UpdatedAt = now
	ticket.StartedAt = &now
	if _, err := conn.ExecContext(ctx, `
UPDATE tickets
SET status = 'in_progress', updated_at = ?, started_at = ?
WHERE id = ? AND status = 'ready'`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), ticket.ID); err != nil {
		return Ticket{}, fmt.Errorf("claim next ticket: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return Ticket{}, fmt.Errorf("commit next ticket claim: %w", err)
	}
	committed = true
	return ticket, nil
}

func (s *Store) Transition(ctx context.Context, id int64, action Action) (Ticket, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("acquire transition connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Ticket{}, fmt.Errorf("begin transition: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	ticket, err := scanTicket(conn.QueryRowContext(ctx, `
	SELECT id, title, summary, spec_path, plan_path, worktree_branch, prompt, status,
       created_at, updated_at, started_at, completed_at, cancelled_at
FROM tickets
WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, fmt.Errorf("get transition ticket: %w", err)
	}

	targets, ok := allowedTransitions[action]
	if !ok {
		return Ticket{}, ErrInvalidTransition
	}
	target, ok := targets[ticket.Status]
	if !ok {
		return Ticket{}, ErrInvalidTransition
	}

	now := s.now().UTC()
	switch action {
	case ActionStart:
		ticket.StartedAt = &now
	case ActionDone:
		ticket.CompletedAt = &now
	case ActionCancel:
		ticket.CancelledAt = &now
	case ActionReopen:
		ticket.StartedAt = nil
		ticket.CompletedAt = nil
		ticket.CancelledAt = nil
	}
	ticket.Status = target
	ticket.UpdatedAt = now
	if _, err := conn.ExecContext(ctx, `
UPDATE tickets
SET status = ?, updated_at = ?, started_at = ?, completed_at = ?, cancelled_at = ?
WHERE id = ?`, ticket.Status, ticket.UpdatedAt.Format(time.RFC3339Nano), optionalTimeValue(ticket.StartedAt), optionalTimeValue(ticket.CompletedAt), optionalTimeValue(ticket.CancelledAt), ticket.ID); err != nil {
		return Ticket{}, fmt.Errorf("update ticket transition: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return Ticket{}, fmt.Errorf("commit ticket transition: %w", err)
	}
	committed = true
	return ticket, nil
}

func (s *Store) Requeue(ctx context.Context, id int64) (Ticket, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("acquire requeue connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Ticket{}, fmt.Errorf("begin requeue: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	ticket, err := scanTicket(conn.QueryRowContext(ctx, `
	SELECT id, title, summary, spec_path, plan_path, worktree_branch, prompt, status,
       created_at, updated_at, started_at, completed_at, cancelled_at
FROM tickets
WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, fmt.Errorf("get requeue ticket: %w", err)
	}
	if ticket.Status != StatusInProgress {
		return Ticket{}, ErrInvalidTransition
	}

	now := s.now().UTC()
	ticket.Status = StatusReady
	ticket.UpdatedAt = now
	ticket.StartedAt = nil
	if _, err := conn.ExecContext(ctx, `
UPDATE tickets
SET status = 'ready', updated_at = ?, started_at = NULL
WHERE id = ? AND status = 'in_progress'`, now.Format(time.RFC3339Nano), id); err != nil {
		return Ticket{}, fmt.Errorf("update requeued ticket: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return Ticket{}, fmt.Errorf("commit ticket requeue: %w", err)
	}
	committed = true
	return ticket, nil
}

var allowedTransitions = map[Action]map[Status]Status{
	ActionStart:  {StatusReady: StatusInProgress},
	ActionDone:   {StatusInProgress: StatusDone},
	ActionCancel: {StatusReady: StatusCancelled, StatusInProgress: StatusCancelled},
	ActionReopen: {StatusDone: StatusReady, StatusCancelled: StatusReady},
}

func (s *Store) artifactPath(input, requiredDirectory string) (string, error) {
	candidate := input
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(s.root, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidArtifact
	}
	required := requiredDirectory + string(filepath.Separator)
	if !strings.HasPrefix(rel, required) || filepath.Ext(rel) != ".md" {
		return "", ErrInvalidArtifact
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrInvalidArtifact
	}
	return filepath.ToSlash(rel), nil
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func scanTicket(scanner interface{ Scan(...any) error }) (Ticket, error) {
	var ticket Ticket
	var status string
	var createdAt, updatedAt string
	var startedAt, completedAt, cancelledAt sql.NullString
	if err := scanner.Scan(
		&ticket.ID,
		&ticket.Title,
		&ticket.Summary,
		&ticket.SpecPath,
		&ticket.PlanPath,
		&ticket.WorktreeBranch,
		&ticket.Prompt,
		&status,
		&createdAt,
		&updatedAt,
		&startedAt,
		&completedAt,
		&cancelledAt,
	); err != nil {
		return Ticket{}, err
	}
	var err error
	ticket.Status = Status(status)
	if ticket.CreatedAt, err = parseTime(createdAt); err != nil {
		return Ticket{}, err
	}
	if ticket.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Ticket{}, err
	}
	if ticket.StartedAt, err = parseOptionalTime(startedAt); err != nil {
		return Ticket{}, err
	}
	if ticket.CompletedAt, err = parseOptionalTime(completedAt); err != nil {
		return Ticket{}, err
	}
	if ticket.CancelledAt, err = parseOptionalTime(cancelledAt); err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}

func validStatus(status Status) bool {
	switch status {
	case StatusReady, StatusInProgress, StatusDone, StatusCancelled:
		return true
	default:
		return false
	}
}

func optionalTimeValue(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Format(time.RFC3339Nano)
}
