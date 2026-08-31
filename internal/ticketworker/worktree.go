package ticketworker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// GitWorktreePreparer creates one persistent worktree for each ticket. The
// worktree is intentionally preserved after completion so its changes and
// branch remain available for review and integration.
type GitWorktreePreparer struct{}

func (GitWorktreePreparer) Prepare(ctx context.Context, root string, ticket Ticket) (string, error) {
	root = strings.TrimSpace(root)
	branch := strings.TrimSpace(ticket.WorktreeBranch)
	if root == "" {
		return "", errors.New("repository root is required")
	}
	if ticket.ID <= 0 {
		return "", errors.New("ticket ID must be positive")
	}
	if branch == "" {
		return "", ErrInvalidWorktreeBranch
	}
	root, err := canonicalPath(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	if _, err := runGitCommand(ctx, root, "check-ref-format", "--branch", branch); err != nil {
		return "", fmt.Errorf("%w %q: %v", ErrInvalidWorktreeBranch, branch, err)
	}

	worktreePath := filepath.Join(root, ".worktrees", "ticket-"+strconv.FormatInt(ticket.ID, 10))
	entries, err := listGitWorktrees(ctx, root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.branch != "refs/heads/"+branch {
			continue
		}
		entryPath, pathErr := canonicalPath(entry.path)
		if pathErr == nil && entryPath == worktreePath {
			return worktreePath, nil
		}
		return "", fmt.Errorf("worktree branch %q is already checked out at %s", branch, entry.path)
	}
	if _, err := os.Lstat(worktreePath); err == nil {
		return "", fmt.Errorf("worktree path already exists but is not registered: %s", worktreePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect worktree path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", fmt.Errorf("create worktree parent: %w", err)
	}

	branchExists, err := localBranchExists(ctx, root, branch)
	if err != nil {
		return "", err
	}
	args := []string{"worktree", "add"}
	if !branchExists {
		args = append(args, "-b", branch)
	}
	args = append(args, worktreePath)
	if branchExists {
		args = append(args, branch)
	} else {
		args = append(args, "HEAD")
	}
	if _, err := runGitCommand(ctx, root, args...); err != nil {
		return "", fmt.Errorf("create worktree for branch %q: %w", branch, err)
	}
	return worktreePath, nil
}

type gitWorktreeEntry struct {
	path   string
	branch string
}

func listGitWorktrees(ctx context.Context, root string) ([]gitWorktreeEntry, error) {
	output, err := runGitCommand(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list git worktrees: %w", err)
	}
	var entries []gitWorktreeEntry
	var current gitWorktreeEntry
	flush := func() {
		if current.path != "" {
			entries = append(entries, current)
		}
		current = gitWorktreeEntry{}
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if value, ok := strings.CutPrefix(line, "worktree "); ok {
			current.path = value
		}
		if value, ok := strings.CutPrefix(line, "branch "); ok {
			current.branch = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse git worktrees: %w", err)
	}
	flush()
	return entries, nil
}

func localBranchExists(ctx context.Context, root, branch string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("inspect worktree branch %q: %w", branch, err)
}

func runGitCommand(ctx context.Context, root string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(abs), nil
	}
	return "", err
}
