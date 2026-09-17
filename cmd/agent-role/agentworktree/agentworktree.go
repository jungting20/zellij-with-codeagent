// Package agentworktree owns worktree creation for interactive agent launches.
package agentworktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"zellij-with-codeagent/internal/transport"
)

// Create branches from the selected working directory's HEAD. Worktrees are
// retained after cancellation or agent exit so user work is never discarded.
func Create(ctx context.Context, cwd string) (string, error) {
	return create(ctx, cwd, filepath.Join(os.TempDir(), "zellij-agent-worktrees"), time.Now())
}

func create(ctx context.Context, cwd, root string, now time.Time) (string, error) {
	return createNamed(ctx, cwd, root, "", now)
}

// CreateNamed creates a branch using the supplied name followed by the current time.
func CreateNamed(ctx context.Context, cwd, branch string) (string, error) {
	if strings.TrimSpace(branch) == "" {
		return "", fmt.Errorf("branch name is required")
	}
	return createNamed(ctx, cwd, filepath.Join(os.TempDir(), "zellij-agent-worktrees"), branch, time.Now())
}

func createNamed(ctx context.Context, cwd, root, branch string, now time.Time) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", fmt.Errorf("agent has no working directory")
	}
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve repository: %w: %s", err, strings.TrimSpace(string(out)))
	}
	repo := strings.TrimSpace(string(out))
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = filepath.Base(repo)
	}
	name := branch + "-" + now.Format("150405")
	if out, err := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", name).CombinedOutput(); err != nil || strings.HasPrefix(name, "@{-") {
		return "", fmt.Errorf("invalid branch name %q: %s", branch, strings.TrimSpace(string(out)))
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	// Keep branch prefixes such as feat/ inside a single directory component.
	path := filepath.Join(root, strings.ReplaceAll(name, "/", "-"))
	out, err = exec.CommandContext(ctx, "git", "-C", repo, "worktree", "add", "-b", name, path, "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("create worktree: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

func Run(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: agent-worktree <path>")
		return 2
	}
	path, err := Create(context.Background(), args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "Worktree: "+path)
	cmd := exec.Command("zellij-agent", "list-selector")
	cmd.Dir = path
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if e, ok := err.(*exec.ExitError); ok {
			return e.ExitCode()
		}
		return 1
	}
	return 0
}

// Start launches the selector's managed-agent command through the daemon.
func Start(ctx context.Context, client interface {
	StartAgent(context.Context, transport.StartAgentRequest) (transport.StartAgentResponse, error)
}, parent transport.AgentWithPane, path string, args []string) error {
	if len(args) < 4 || args[0] != "agent" || args[1] != "start" {
		return fmt.Errorf("unsupported selector command")
	}
	req := transport.StartAgentRequest{
		Kind: args[2], CWD: path, ParentPaneID: parent.Pane.ID,
		TargetSession: "worktree-agent",
		SourceSession: parent.Pane.SessionID, SourceZellijPaneID: parent.Pane.ZellijPaneID,
	}
	for i := 3; i < len(args); i++ {
		switch args[i] {
		case "--notify-idle":
			req.NotifyOnIdle = true
		case "--":
			req.Args = append([]string(nil), args[i+1:]...)
			i = len(args)
		default:
			return fmt.Errorf("unsupported selector option %q", args[i])
		}
	}
	if req.ParentPaneID == "" {
		return fmt.Errorf("agent has no managed parent pane")
	}
	_, err := client.StartAgent(ctx, req)
	return err
}
