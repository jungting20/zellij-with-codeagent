package ticketworker

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRootFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "tools", "worker")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindRoot(nested)
	if err != nil || got != root {
		t.Fatalf("FindRoot() = %q, %v; want %q", got, err, root)
	}
}

func TestFindRootAcceptsWorktreeGitFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /tmp/example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FindRoot(root)
	if err != nil || got != root {
		t.Fatalf("FindRoot() = %q, %v; want %q", got, err, root)
	}
}

func TestFindRootRejectsDirectoryOutsideRepository(t *testing.T) {
	if _, err := FindRoot(t.TempDir()); !errors.Is(err, ErrRepositoryNotFound) {
		t.Fatalf("FindRoot() error = %v, want ErrRepositoryNotFound", err)
	}
}

func TestDatabasePathUsesTicketWorkerDirectory(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	want := filepath.Join(root, ".zellij-agent", "ticket-worker", "tickets.db")
	if got := DatabasePath(root); got != want {
		t.Fatalf("DatabasePath() = %q, want %q", got, want)
	}
}

func TestOpenExistingRequiresInitializationWithoutCreatingDatabase(t *testing.T) {
	root := newRepositoryRoot(t)
	_, err := OpenExisting(context.Background(), root, nil)
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("OpenExisting() error = %v, want ErrNotInitialized", err)
	}
	if _, err := os.Stat(DatabasePath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database stat error = %v, want not exist", err)
	}
}

func TestInitializeProjectIsIdempotentAndUpdatesGitignoreOnce(t *testing.T) {
	root := newRepositoryRoot(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("bin/"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InitializeProject(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig() after init error = %v", err)
	}
	if cfg.MaxWorkers != 3 || cfg.PollInterval.String() != "30s" {
		t.Fatalf("initialized config = %+v", cfg)
	}
	customConfig := "version: 1\nmax_workers: 2\npoll_interval: 1m\nprompt_template: custom {{ .Title }}\n"
	if err := os.WriteFile(ConfigPath(root), []byte(customConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InitializeProject(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(preserved) != customConfig {
		t.Fatalf("config after second init = %q, want %q", preserved, customConfig)
	}
	db, err := sql.Open("sqlite", DatabasePath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("schema version = %d, error = %v; want 1", version, err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), ignoreEntry); got != 1 {
		t.Fatalf(".gitignore = %q, entry count = %d", data, got)
	}
}

func TestInitializeProjectConfigFailurePreservesDatabase(t *testing.T) {
	root := newRepositoryRoot(t)
	workerPath := filepath.Join(root, ".zellij-agent", "worker")
	if err := os.MkdirAll(filepath.Dir(workerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workerPath, []byte("blocks config directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := InitializeProject(context.Background(), root, nil)
	if err == nil || !strings.Contains(err.Error(), "ticket-worker config") {
		t.Fatalf("InitializeProject() error = %v, want config initialization error", err)
	}
	if _, err := os.Stat(DatabasePath(root)); err != nil {
		t.Fatalf("database stat after config failure = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ignoreEntry) {
		t.Fatalf(".gitignore after config failure = %q, missing %q", data, ignoreEntry)
	}
}

func TestInitializeProjectPreservesExistingTickets(t *testing.T) {
	root := newRepositoryRoot(t)
	if err := InitializeProject(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	store, err := OpenExisting(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO tickets(title, summary, spec_path, plan_path, status, created_at, updated_at) VALUES ('Title', 'Summary', 'docs/superpowers/specs/example-design.md', 'docs/superpowers/plans/example.md', 'ready', '2026-07-17T00:00:00Z', '2026-07-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := InitializeProject(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	store, err = OpenExisting(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM tickets").Scan(&count); err != nil || count != 1 {
		t.Fatalf("ticket count = %d, error = %v; want 1", count, err)
	}
}

func newRepositoryRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}
