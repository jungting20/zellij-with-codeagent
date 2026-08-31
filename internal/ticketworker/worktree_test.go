package ticketworker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitWorktreePreparerCreatesAndReusesTicketBranch(t *testing.T) {
	root := initializeGitRepository(t)
	preparer := GitWorktreePreparer{}
	ticket := Ticket{ID: 7, WorktreeBranch: "feat/ticket-seven"}

	worktree, err := preparer.Prepare(context.Background(), root, ticket)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, ".worktrees", "ticket-7")
	if worktree != want {
		t.Fatalf("worktree = %q, want %q", worktree, want)
	}
	if got := runGit(t, worktree, "branch", "--show-current"); got != ticket.WorktreeBranch {
		t.Fatalf("branch = %q, want %q", got, ticket.WorktreeBranch)
	}
	if reused, err := preparer.Prepare(context.Background(), root, ticket); err != nil || reused != want {
		t.Fatalf("reuse = %q, %v", reused, err)
	}
}

func TestGitWorktreePreparerUsesExistingUnattachedBranch(t *testing.T) {
	root := initializeGitRepository(t)
	runGit(t, root, "branch", "ticket/existing")

	worktree, err := (GitWorktreePreparer{}).Prepare(context.Background(), root, Ticket{ID: 8, WorktreeBranch: "ticket/existing"})
	if err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, worktree, "branch", "--show-current"); got != "ticket/existing" {
		t.Fatalf("branch = %q", got)
	}
}

func TestGitWorktreePreparerRejectsInvalidOrConflictingBranch(t *testing.T) {
	root := initializeGitRepository(t)
	preparer := GitWorktreePreparer{}
	if _, err := preparer.Prepare(context.Background(), root, Ticket{ID: 9, WorktreeBranch: "../escape"}); err == nil {
		t.Fatal("invalid branch error = nil")
	}
	if _, err := preparer.Prepare(context.Background(), root, Ticket{ID: 10, WorktreeBranch: runGit(t, root, "branch", "--show-current")}); err == nil || !strings.Contains(err.Error(), "already checked out") {
		t.Fatalf("checked-out branch error = %v", err)
	}
}

func initializeGitRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "initial")
	return root
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
