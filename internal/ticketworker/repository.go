package ticketworker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrRepositoryNotFound = errors.New("repository root not found")

const ignoreEntry = ".zellij-agent/ticket-worker/"

func FindRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect repository marker: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", ErrRepositoryNotFound
		}
		current = parent
	}
}

func DatabasePath(root string) string {
	return filepath.Join(root, ".zellij-agent", "ticket-worker", "tickets.db")
}

func InitializeProject(ctx context.Context, root string, now func() time.Time) error {
	store, err := openStore(ctx, root, DatabasePath(root), now, true)
	if err != nil {
		return err
	}
	if err := store.Close(); err != nil {
		return fmt.Errorf("close ticket database: %w", err)
	}
	return ensureGitignore(root)
}

func OpenExisting(ctx context.Context, root string, now func() time.Time) (*Store, error) {
	databasePath := DatabasePath(root)
	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	} else if err != nil {
		return nil, fmt.Errorf("inspect ticket database: %w", err)
	}
	return openStore(ctx, root, databasePath, now, false)
}

func ensureGitignore(root string) error {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == ignoreEntry {
			return nil
		}
	}
	updated := string(data)
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += ignoreEntry + "\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("update .gitignore: %w", err)
	}
	return nil
}
