package ticketworker

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 7, 16, 12, 0, 0, 123, time.UTC)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(context.Background(), root, filepath.Join(root, ".local", "tickets.db"), func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, root
}

func writeArtifacts(t *testing.T, root, name string) (string, string) {
	t.Helper()
	spec := filepath.Join(root, "docs", "superpowers", "specs", name+"-design.md")
	plan := filepath.Join(root, "docs", "superpowers", "plans", name+".md")
	for _, path := range []string{spec, plan} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# Approved\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return spec, plan
}

func TestOpenCreatesSchemaAndAddRegistersReadyTicketWithoutArtifacts(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "search")

	got, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
		Title:    "Search story bible",
		Summary:  "Add indexed story-bible search.",
		SpecPath: spec,
		PlanPath: plan,
		Prompt:   "Implement search.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusReady || got.ID != 1 {
		t.Fatalf("Add() = %#v", got)
	}
	if got.SpecPath != "" || got.PlanPath != "" {
		t.Fatalf("stored paths = %q, %q; want empty", got.SpecPath, got.PlanPath)
	}
	if !got.CreatedAt.Equal(fixedNow) || !got.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("timestamps = %s, %s", got.CreatedAt, got.UpdatedAt)
	}
}

func TestAddStoresTrimmedMultilinePrompt(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "prompt")
	created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
		Title: "Prompt", Summary: "Store it", SpecPath: spec, PlanPath: plan,
		Prompt: "  first line\nsecond line  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Prompt != "first line\nsecond line" {
		t.Fatalf("Prompt = %q", created.Prompt)
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompt != created.Prompt {
		t.Fatalf("Get().Prompt = %q", got.Prompt)
	}
}

func TestAddDefaultsAndValidatesAgent(t *testing.T) {
	store, _ := newTestStore(t)
	created, err := store.Add(context.Background(), CreateInput{
		Title: "Default agent", Summary: "Use Codex", WorktreeBranch: "ticket/default-agent", Prompt: "Implement.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Agent != "codex" {
		t.Fatalf("Agent = %q, want codex", created.Agent)
	}
	created, err = store.Add(context.Background(), CreateInput{
		Title: "Claude agent", Summary: "Use Claude", WorktreeBranch: "ticket/claude-agent", Prompt: "Implement.", Agent: " claude ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Agent != "claude" {
		t.Fatalf("Agent = %q, want claude", created.Agent)
	}
	if _, err := store.Add(context.Background(), CreateInput{
		Title: "Bad agent", Summary: "Reject it", WorktreeBranch: "ticket/bad-agent", Prompt: "Implement.", Agent: "unknown",
	}); !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("Add() error = %v, want ErrInvalidAgent", err)
	}
}

func TestAddRejectsInvalidPrompt(t *testing.T) {
	store, root := newTestStore(t)
	tests := []struct {
		name   string
		prompt string
	}{
		{name: "empty", prompt: ""},
		{name: "whitespace", prompt: "   "},
		{name: "marker", prompt: "work\nZELLIJ_AGENT_TICKET_DONE 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, plan := writeArtifacts(t, root, "invalid-prompt-"+tt.name)
			_, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
				Title: "Prompt", Summary: "Validate it", SpecPath: spec,
				PlanPath: plan, Prompt: tt.prompt,
			})
			if !errors.Is(err, ErrInvalidPrompt) {
				t.Fatalf("Add(prompt=%q) error = %v", tt.prompt, err)
			}
		})
	}
}

func TestOpenRejectsVersion1WithoutMigration(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "tickets.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(context.Background(), root, databasePath, nil); err == nil {
		t.Fatal("Open() error = nil, want version 1 rejection")
	} else if !strings.Contains(err.Error(), "remove") || !strings.Contains(err.Error(), "ticket-worker init") {
		t.Fatalf("Open() error = %q, want reinitialization guidance", err)
	}

	db, err = sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("schema version = %d, want unchanged version 1", version)
	}
}

func TestOpenInitializesSchemaIdempotently(t *testing.T) {
	store, root := newTestStore(t)
	databasePath := filepath.Join(root, ".local", "tickets.db")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	spec, plan := writeArtifacts(t, root, "idempotent")
	if _, err := second.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
		Title:    "Idempotent schema",
		Summary:  "Open an initialized ticket database.",
		SpecPath: spec,
		PlanPath: plan,
		Prompt:   "Verify idempotency.",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenMigratesVersion2AndPreservesTickets(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "before-migration")
	created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
		Title: "Existing", Summary: "Preserve this ticket", SpecPath: spec, PlanPath: plan, Prompt: "Keep this ticket.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, ".local", "tickets.db")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	got, err := migrated.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Schema versions before v4 did not store a worktree branch.
	created.WorktreeBranch = ""
	if got != created {
		t.Fatalf("migrated ticket = %#v, want %#v", got, created)
	}
	for _, title := range []string{"Fast one", "Fast two"} {
		if _, err := migrated.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: title, Summary: "After migration", Prompt: "Implement."}); err != nil {
			t.Fatalf("Add(%q) after migration error = %v", title, err)
		}
	}
	var version int
	if err := migrated.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
}

func TestOpenMigratesVersion4TicketsToDefaultCodexAgent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "tickets.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE tickets (
 id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, summary TEXT NOT NULL,
 spec_path TEXT NOT NULL, plan_path TEXT NOT NULL, worktree_branch TEXT NOT NULL,
 prompt TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 started_at TEXT, completed_at TEXT, cancelled_at TEXT
);
INSERT INTO tickets(title, summary, spec_path, plan_path, worktree_branch, prompt, status, created_at, updated_at)
VALUES ('Existing', 'Version four ticket', '', '', 'ticket/existing', 'Implement.', 'ready', '2026-07-17T00:00:00Z', '2026-07-17T00:00:00Z');
PRAGMA user_version = 4;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(context.Background(), root, databasePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ticket, err := store.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Agent != "codex" {
		t.Fatalf("migrated Agent = %q, want codex", ticket.Agent)
	}
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version = %d, error = %v; want %d", version, err, currentSchemaVersion)
	}
}

func TestAddRequiresAndTrimsWorktreeBranch(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "worktree-branch")

	if _, err := store.Add(context.Background(), CreateInput{
		Title: "Missing branch", Summary: "Reject an empty branch", SpecPath: spec, PlanPath: plan, Prompt: "Implement.",
	}); !errors.Is(err, ErrInvalidWorktreeBranch) {
		t.Fatalf("Add() error = %v, want ErrInvalidWorktreeBranch", err)
	}

	created, err := store.Add(context.Background(), CreateInput{
		Title: "Branch", Summary: "Store the branch", SpecPath: spec, PlanPath: plan,
		WorktreeBranch: "  feat/ticket-worktree  ", Prompt: "Implement.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.WorktreeBranch != "feat/ticket-worktree" {
		t.Fatalf("WorktreeBranch = %q", created.WorktreeBranch)
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorktreeBranch != created.WorktreeBranch {
		t.Fatalf("Get().WorktreeBranch = %q, want %q", got.WorktreeBranch, created.WorktreeBranch)
	}
}

func TestOpenRejectsUnsupportedFutureSchemaVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, ".local", "tickets.db")
	store, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA user_version = 6"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow }); err == nil {
		t.Fatal("Open() error = nil, want unsupported future schema rejection")
	} else if got, want := err.Error(), "unsupported ticket schema version 6"; got != want {
		t.Fatalf("Open() error = %q, want %q", got, want)
	}
}

func TestAddIgnoresLegacyArtifactPaths(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "relative")
	relativeSpec, err := filepath.Rel(root, spec)
	if err != nil {
		t.Fatal(err)
	}
	relativePlan, err := filepath.Rel(root, plan)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
		Title:    "Relative artifacts",
		Summary:  "Register repository-relative artifact paths.",
		SpecPath: relativeSpec,
		PlanPath: relativePlan,
		Prompt:   "Use relative artifacts.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SpecPath != "" || got.PlanPath != "" {
		t.Fatalf("stored paths = %q, %q; want empty", got.SpecPath, got.PlanPath)
	}
}

func TestAddAcceptsRepeatedLegacyPlanPath(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "search")
	input := CreateInput{WorktreeBranch: "ticket/test", Title: "Search", Summary: "Search stories.", SpecPath: spec, PlanPath: plan, Prompt: "Implement search."}
	if _, err := store.Add(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(context.Background(), input); err != nil {
		t.Fatalf("second Add() error = %v", err)
	}
}

func TestAddAcceptsMultipleTicketsWithoutArtifacts(t *testing.T) {
	store, _ := newTestStore(t)
	for i, title := range []string{"First", "Second"} {
		created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
			Title: title, Summary: "No artifact ticket", Prompt: "Implement " + title + ".",
		})
		if err != nil {
			t.Fatalf("Add(%d) error = %v", i, err)
		}
		if created.SpecPath != "" || created.PlanPath != "" {
			t.Fatalf("Add(%d) paths = %q, %q; want empty", i, created.SpecPath, created.PlanPath)
		}
	}
}

func TestAddRejectsEmptyTitleAndSummary(t *testing.T) {
	store, _ := newTestStore(t)
	cases := []CreateInput{
		{Title: "", Summary: "summary", WorktreeBranch: "ticket/test", Prompt: "Implement."},
		{Title: "title", Summary: "", WorktreeBranch: "ticket/test", Prompt: "Implement."},
	}
	for _, input := range cases {
		if _, err := store.Add(context.Background(), input); !errors.Is(err, ErrInvalidArtifact) {
			t.Fatalf("Add(%#v) error = %v, want ErrInvalidArtifact", input, err)
		}
	}
}

func TestGetReturnsTicketAndRejectsUnknownID(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "get")
	created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
		Title:    "Get ticket",
		Summary:  "Retrieve one ticket.",
		SpecPath: spec,
		PlanPath: plan,
		Prompt:   "Retrieve this ticket.",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != created {
		t.Fatalf("Get() = %#v, want %#v", got, created)
	}
	if _, err := store.Get(context.Background(), created.ID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestListFiltersStatusesAndOrdersFIFOTiesByID(t *testing.T) {
	store, root := newTestStore(t)
	firstSpec, firstPlan := writeArtifacts(t, root, "first")
	first, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "First", Summary: "First summary", SpecPath: firstSpec, PlanPath: firstPlan, Prompt: "Implement first."})
	if err != nil {
		t.Fatal(err)
	}
	secondSpec, secondPlan := writeArtifacts(t, root, "second")
	second, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Second", Summary: "Second summary", SpecPath: secondSpec, PlanPath: secondPlan, Prompt: "Implement second."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), second.ID, ActionStart); err != nil {
		t.Fatal(err)
	}

	all, err := store.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != first.ID || all[1].ID != second.ID {
		t.Fatalf("List(nil) = %#v", all)
	}

	ready, err := store.List(context.Background(), statusPtr(StatusReady))
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].ID != first.ID {
		t.Fatalf("List(ready) = %#v", ready)
	}

	inProgress, err := store.List(context.Background(), statusPtr(StatusInProgress))
	if err != nil {
		t.Fatal(err)
	}
	if len(inProgress) != 1 || inProgress[0].ID != second.ID {
		t.Fatalf("List(in_progress) = %#v", inProgress)
	}

	invalid := Status("unknown")
	if _, err := store.List(context.Background(), &invalid); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("List(unknown) error = %v, want ErrInvalidStatus", err)
	}
}

func TestListReturnsAllocatedEmptySlice(t *testing.T) {
	store, _ := newTestStore(t)

	got, err := store.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List(nil) = %#v, want allocated empty slice", got)
	}
}

func TestNextClaimsOldestReady(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := fixedNow
	store, err := Open(context.Background(), root, filepath.Join(root, ".local", "tickets.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spec1, plan1 := writeArtifacts(t, root, "first")
	first, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "First", Summary: "First summary", SpecPath: spec1, PlanPath: plan1, Prompt: "Implement first."})
	if err != nil {
		t.Fatal(err)
	}
	spec2, plan2 := writeArtifacts(t, root, "second")
	if _, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Second", Summary: "Second summary", SpecPath: spec2, PlanPath: plan2, Prompt: "Implement second."}); err != nil {
		t.Fatal(err)
	}

	now = fixedNow.Add(time.Minute)
	got, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || got.Status != StatusInProgress || got.StartedAt == nil || !got.StartedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("Next() = %#v", got)
	}
	after, err := store.Get(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, got) {
		t.Fatalf("claimed ticket not persisted: returned=%#v stored=%#v", got, after)
	}
	remaining, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if remaining.ID == first.ID || remaining.Status != StatusInProgress {
		t.Fatalf("second Next() = %#v", remaining)
	}
}

func TestNextReturnsEmptyQueueAfterReadyTicketsLeaveQueue(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "first")
	created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "First", Summary: "First summary", SpecPath: spec, PlanPath: plan, Prompt: "Implement first."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), created.ID, ActionStart); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Next(context.Background()); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("Next() error = %v, want ErrEmptyQueue", err)
	}
}

func TestRequeueMovesInProgressTicketToReadyAndClearsStartedAt(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := fixedNow
	store, err := Open(context.Background(), root, filepath.Join(root, ".local", "tickets.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spec, plan := writeArtifacts(t, root, "requeue")
	created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Retry", Summary: "Retry safely", SpecPath: spec, PlanPath: plan, Prompt: "Retry safely."})
	if err != nil {
		t.Fatal(err)
	}
	now = fixedNow.Add(time.Minute)
	claimed, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now = fixedNow.Add(2 * time.Minute)
	requeued, err := store.Requeue(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requeued.Status != StatusReady || requeued.StartedAt != nil || !requeued.UpdatedAt.Equal(now) {
		t.Fatalf("Requeue() = %#v", requeued)
	}
	if requeued.Title != created.Title || requeued.SpecPath != created.SpecPath || requeued.PlanPath != created.PlanPath {
		t.Fatalf("Requeue() changed ticket fields: %#v", requeued)
	}
	persisted, err := store.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, requeued) {
		t.Fatalf("persisted = %#v, returned = %#v", persisted, requeued)
	}
}

func TestRequeueRejectsMissingAndNonInProgressTickets(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "requeue-invalid")
	created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Ready", Summary: "Stay ready", SpecPath: spec, PlanPath: plan, Prompt: "Stay ready."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Requeue(context.Background(), created.ID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Requeue(missing) error = %v", err)
	}
	if _, err := store.Requeue(context.Background(), created.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Requeue(ready) error = %v", err)
	}
}

func TestConcurrentNextClaimsTicketOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, ".local", "tickets.db")
	first, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	spec, plan := writeArtifacts(t, root, "only")
	created, err := first.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Only", Summary: "Only summary", SpecPath: spec, PlanPath: plan, Prompt: "Implement only."})
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		ticket Ticket
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, store := range []*Store{first, second} {
		go func(store *Store) {
			<-start
			ticket, err := store.Next(context.Background())
			results <- result{ticket: ticket, err: err}
		}(store)
	}
	close(start)

	claimed := 0
	empty := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			claimed++
			if result.ticket.ID != created.ID || result.ticket.Status != StatusInProgress {
				t.Fatalf("claimed ticket = %#v", result.ticket)
			}
		case errors.Is(result.err, ErrEmptyQueue):
			empty++
		default:
			t.Fatalf("Next() error = %v", result.err)
		}
	}
	if claimed != 1 || empty != 1 {
		t.Fatalf("claimed=%d empty=%d, want 1 each", claimed, empty)
	}
}

func TestLifecycleTransitionsUseAdvancingClockAndResetTimestamps(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := fixedNow
	store, err := Open(context.Background(), root, filepath.Join(root, ".local", "tickets.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spec, plan := writeArtifacts(t, root, "flow")
	created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Flow", Summary: "Flow summary", SpecPath: spec, PlanPath: plan, Prompt: "Implement flow."})
	if err != nil {
		t.Fatal(err)
	}
	if !created.CreatedAt.Equal(fixedNow) || !created.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("created = %#v", created)
	}

	now = fixedNow.Add(time.Minute)
	started, err := store.Transition(context.Background(), created.ID, ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != StatusInProgress || started.StartedAt == nil || !started.StartedAt.Equal(now) || !started.UpdatedAt.Equal(now) || !started.CreatedAt.Equal(fixedNow) {
		t.Fatalf("start = %#v", started)
	}

	now = fixedNow.Add(2 * time.Minute)
	done, err := store.Transition(context.Background(), created.ID, ActionDone)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusDone || done.StartedAt == nil || !done.StartedAt.Equal(fixedNow.Add(time.Minute)) || done.CompletedAt == nil || !done.CompletedAt.Equal(now) || done.CancelledAt != nil || !done.UpdatedAt.Equal(now) {
		t.Fatalf("done = %#v", done)
	}

	now = fixedNow.Add(3 * time.Minute)
	reopened, err := store.Transition(context.Background(), created.ID, ActionReopen)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != StatusReady || reopened.StartedAt != nil || reopened.CompletedAt != nil || reopened.CancelledAt != nil || !reopened.UpdatedAt.Equal(now) {
		t.Fatalf("reopen = %#v", reopened)
	}

	now = fixedNow.Add(4 * time.Minute)
	cancelled, err := store.Transition(context.Background(), created.ID, ActionCancel)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != StatusCancelled || cancelled.StartedAt != nil || cancelled.CompletedAt != nil || cancelled.CancelledAt == nil || !cancelled.CancelledAt.Equal(now) || !cancelled.UpdatedAt.Equal(now) {
		t.Fatalf("cancel = %#v", cancelled)
	}

	now = fixedNow.Add(5 * time.Minute)
	reopened, err = store.Transition(context.Background(), created.ID, ActionReopen)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != StatusReady || reopened.StartedAt != nil || reopened.CompletedAt != nil || reopened.CancelledAt != nil || !reopened.UpdatedAt.Equal(now) {
		t.Fatalf("second reopen = %#v", reopened)
	}
}

func TestLifecycleAllowsCancellationAndReopening(t *testing.T) {
	cases := []struct {
		name    string
		actions []Action
		status  Status
	}{
		{name: "ready to cancelled", actions: []Action{ActionCancel}, status: StatusCancelled},
		{name: "in progress to cancelled", actions: []Action{ActionStart, ActionCancel}, status: StatusCancelled},
		{name: "cancelled to ready", actions: []Action{ActionCancel, ActionReopen}, status: StatusReady},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, root := newTestStore(t)
			spec, plan := writeArtifacts(t, root, "flow")
			created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Flow", Summary: "Flow summary", SpecPath: spec, PlanPath: plan, Prompt: "Implement flow."})
			if err != nil {
				t.Fatal(err)
			}

			var got Ticket
			for _, action := range tc.actions {
				got, err = store.Transition(context.Background(), created.ID, action)
				if err != nil {
					t.Fatal(err)
				}
			}
			if got.Status != tc.status {
				t.Fatalf("Transition() status = %q, want %q", got.Status, tc.status)
			}
			if tc.status == StatusCancelled && (got.CancelledAt == nil || !got.CancelledAt.Equal(fixedNow)) {
				t.Fatalf("cancelled ticket = %#v", got)
			}
			if tc.status == StatusReady && (got.StartedAt != nil || got.CompletedAt != nil || got.CancelledAt != nil) {
				t.Fatalf("reopened ticket = %#v", got)
			}
		})
	}
}

func TestConcurrentTransitionsFromIndependentStoresSerialize(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, ".local", "tickets.db")
	firstHasLock := make(chan struct{})
	releaseFirst := make(chan struct{})
	clockCalls := 0
	firstStore, err := Open(context.Background(), root, databasePath, func() time.Time {
		clockCalls++
		if clockCalls == 3 {
			close(firstHasLock)
			<-releaseFirst
		}
		return fixedNow.Add(time.Duration(clockCalls) * time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = firstStore.Close() })

	firstSpec, firstPlan := writeArtifacts(t, root, "concurrent-first")
	first, err := firstStore.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
		Title: "First", Summary: "First concurrent transition", SpecPath: firstSpec, PlanPath: firstPlan, Prompt: "Implement first.",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondSpec, secondPlan := writeArtifacts(t, root, "concurrent-second")
	second, err := firstStore.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test",
		Title: "Second", Summary: "Second concurrent transition", SpecPath: secondSpec, PlanPath: secondPlan, Prompt: "Implement second.",
	})
	if err != nil {
		t.Fatal(err)
	}

	secondStore, err := Open(context.Background(), root, databasePath, func() time.Time {
		return fixedNow.Add(10 * time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := firstStore.Transition(ctx, first.ID, ActionStart)
		firstDone <- err
	}()
	select {
	case <-firstHasLock:
	case <-ctx.Done():
		t.Fatalf("first transition did not reach its locked section: %v", ctx.Err())
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, err := secondStore.Transition(ctx, second.ID, ActionStart)
		secondDone <- err
	}()
	<-secondStarted

	var secondErr error
	secondFinished := false
	select {
	case secondErr = <-secondDone:
		secondFinished = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)

	select {
	case err := <-firstDone:
		if err != nil {
			t.Errorf("first transition: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("first transition timed out: %v", ctx.Err())
	}
	if !secondFinished {
		select {
		case secondErr = <-secondDone:
		case <-ctx.Done():
			t.Fatalf("second transition timed out: %v", ctx.Err())
		}
	}
	if secondErr != nil {
		t.Errorf("second transition: %v", secondErr)
	}
}

func TestEveryInvalidTransitionAndActionRollsBack(t *testing.T) {
	allActions := []Action{ActionStart, ActionDone, ActionCancel, ActionReopen, Action("unknown")}
	states := []struct {
		status Status
		setup  []Action
		valid  map[Action]bool
	}{
		{status: StatusReady, valid: map[Action]bool{ActionStart: true, ActionCancel: true}},
		{status: StatusInProgress, setup: []Action{ActionStart}, valid: map[Action]bool{ActionDone: true, ActionCancel: true}},
		{status: StatusDone, setup: []Action{ActionStart, ActionDone}, valid: map[Action]bool{ActionReopen: true}},
		{status: StatusCancelled, setup: []Action{ActionCancel}, valid: map[Action]bool{ActionReopen: true}},
	}

	for _, state := range states {
		for _, action := range allActions {
			if state.valid[action] {
				continue
			}
			t.Run(string(state.status)+"/"+string(action), func(t *testing.T) {
				store, root := newTestStore(t)
				spec, plan := writeArtifacts(t, root, "invalid")
				created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Invalid", Summary: "Invalid transition", SpecPath: spec, PlanPath: plan, Prompt: "Invalid transition."})
				if err != nil {
					t.Fatal(err)
				}
				for _, setupAction := range state.setup {
					if _, err := store.Transition(context.Background(), created.ID, setupAction); err != nil {
						t.Fatalf("setup %q: %v", setupAction, err)
					}
				}
				before, err := store.Get(context.Background(), created.ID)
				if err != nil {
					t.Fatal(err)
				}

				if _, err := store.Transition(context.Background(), created.ID, action); !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("Transition(%q, %q) error = %v, want ErrInvalidTransition", state.status, action, err)
				}
				after, err := store.Get(context.Background(), created.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("invalid transition mutated ticket: before=%#v after=%#v", before, after)
				}
			})
		}
	}
}

func TestTransitionRejectsUnknownIDsAndActions(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "flow")
	created, err := store.Add(context.Background(), CreateInput{WorktreeBranch: "ticket/test", Title: "Flow", Summary: "Flow summary", SpecPath: spec, PlanPath: plan, Prompt: "Implement flow."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), created.ID+1, ActionStart); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Transition(unknown ID) error = %v, want ErrNotFound", err)
	}
	if _, err := store.Transition(context.Background(), created.ID, Action("unknown")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Transition(unknown action) error = %v, want ErrInvalidTransition", err)
	}
	after, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != created {
		t.Fatalf("unknown action mutated ticket: before=%#v after=%#v", created, after)
	}
}

func statusPtr(status Status) *Status { return &status }
