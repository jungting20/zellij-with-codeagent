package codingagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func Run(args []string) int {
	cmd, err := prepare(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "Error running codex: %v\n", err)
		return 1
	}
	return 0
}

func prepare(args []string) (*exec.Cmd, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("usage: agent-role coding-agent <path>")
	}

	repoPath, err := resolveRepositoryPath(args[0])
	if err != nil {
		return nil, err
	}

	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex executable not found on PATH")
	}

	cmd := exec.Command(codexPath)
	cmd.Dir = repoPath
	return cmd, nil
}

func resolveRepositoryPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("path %q is not accessible: %w", absPath, err)
	}
	searchPath := absPath
	if !info.IsDir() {
		searchPath = filepath.Dir(absPath)
	}

	for {
		if _, err := os.Stat(filepath.Join(searchPath, ".git")); err == nil {
			return searchPath, nil
		}
		parent := filepath.Dir(searchPath)
		if parent == searchPath {
			break
		}
		searchPath = parent
	}

	return "", fmt.Errorf("path %q is not inside a git repository", absPath)
}
