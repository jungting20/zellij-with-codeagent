// Package agentworktreesend executes shell commands in a repository's worktrees.
package agentworktreesend

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"zellij-with-codeagent/internal/transport"
)

type Result struct {
	Path   string `json:"path"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// Execute includes the main checkout and linked worktrees returned by Git.
// Each command runs in its own shell; one failure does not stop other worktrees.
func Execute(ctx context.Context, directory, command string) ([]Result, error) {
	if strings.TrimSpace(directory) == "" || strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("working directory and shell command are required")
	}
	out, err := exec.CommandContext(ctx, "git", "-C", directory, "worktree", "list", "--porcelain", "-z").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w: %s", err, out)
	}
	var paths []string
	for _, field := range bytes.Split(out, []byte{0}) {
		if path, ok := strings.CutPrefix(string(field), "worktree "); ok {
			paths = append(paths, path)
		}
	}
	return executePaths(ctx, paths, command)
}

type AgentLister interface {
	ListAgents(context.Context) (transport.ListAgentsResponse, error)
}

// ExecuteChildren resolves the selected parent's current direct child agents.
// It never falls back to executing across the repository when no children exist.
func ExecuteChildren(ctx context.Context, client AgentLister, parentID, command string) ([]Result, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("shell command is required")
	}
	list, err := client.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	var parent transport.AgentWithPane
	for _, row := range list.Agents {
		if row.Agent.ID == parentID {
			parent = row
			break
		}
	}
	if parent.Pane.ID == "" || parent.Pane.CWD == "" {
		return nil, fmt.Errorf("선택한 부모 agent를 찾을 수 없습니다")
	}
	parentPath, err := canonicalDirectory(parent.Pane.CWD)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, row := range list.Agents {
		if row.Pane.ParentPaneID != parent.Pane.ID || row.Pane.CWD == "" {
			continue
		}
		if row.Pane.Status != "running" && row.Pane.Status != "starting" {
			continue
		}
		path, err := canonicalDirectory(row.Pane.CWD)
		if err != nil {
			return nil, err
		}
		if path != parentPath {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("실행할 자식 worktree agent가 없습니다")
	}
	return executePaths(ctx, paths, command)
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func executePaths(ctx context.Context, paths []string, command string) ([]Result, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	var results []Result
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		cmd := exec.CommandContext(ctx, shell, "-c", command)
		cmd.Dir = path
		cmd.WaitDelay = time.Second
		output, err := cmd.CombinedOutput()
		r := Result{Path: path, Output: string(output)}
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no worktrees found")
	}
	return results, nil
}
func Run(args []string) int {
	flags := flag.NewFlagSet("agent-worktree-send", flag.ContinueOnError)
	timeout := flags.Duration("timeout", 5*time.Minute, "total execution timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 || *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "Usage: agent-worktree-send [--timeout DURATION] <path> <shell-command>")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	results, err := Execute(ctx, flags.Arg(0), flags.Arg(1))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, r := range results {
		if r.Error != "" {
			return 1
		}
	}
	return 0
}
