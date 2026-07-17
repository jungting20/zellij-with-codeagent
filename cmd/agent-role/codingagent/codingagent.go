package codingagent

import (
	"errors"
	"flag"
	"fmt"
	"io"
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
	fs := flag.NewFlagSet("coding-agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yolo := fs.Bool("yolo", false, "bypass Codex approvals and sandboxing")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return nil, fmt.Errorf("usage: agent-role coding-agent [--yolo] <path>")
	}

	repoPath, err := resolveRepositoryPath(fs.Arg(0))
	if err != nil {
		return nil, err
	}

	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex executable not found on PATH")
	}

	var codexArgs []string
	if *yolo {
		codexArgs = append(codexArgs, "--dangerously-bypass-approvals-and-sandbox")
	}
	cmd := exec.Command(codexPath, codexArgs...)
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
