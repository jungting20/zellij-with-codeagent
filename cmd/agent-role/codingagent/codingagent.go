package codingagent

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	agentprofile "zellij-with-codeagent/internal/codingagent"
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
		fmt.Fprintf(os.Stderr, "Error running coding agent: %v\n", err)
		return 1
	}
	return 0
}

func prepare(args []string) (*exec.Cmd, error) {
	roleArgs, extraArgs := splitArgs(args)
	fs := flag.NewFlagSet("coding-agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agent := fs.String("agent", string(agentprofile.KindCodex), "coding agent kind")
	yolo := fs.Bool("yolo", false, "bypass coding agent permissions and sandboxing")
	if err := fs.Parse(roleArgs); err != nil || fs.NArg() != 1 {
		return nil, fmt.Errorf("usage: agent-role coding-agent [--agent kind] [--yolo] <path> [-- agent-args...]")
	}

	repoPath, err := resolveRepositoryPath(fs.Arg(0))
	if err != nil {
		return nil, err
	}

	kind, err := agentprofile.ParseKind(*agent)
	if err != nil {
		return nil, err
	}
	profile, ok := agentprofile.LookupProfile(kind)
	if !ok {
		return nil, fmt.Errorf("coding agent profile %q not found", kind)
	}

	agentPath, err := exec.LookPath(profile.Executable)
	if err != nil {
		return nil, fmt.Errorf("%s executable not found on PATH", profile.Executable)
	}

	commandArgs := profile.BuildCommand(*yolo, extraArgs)
	commandArgs[0] = agentPath
	cmd := exec.Command(commandArgs[0], commandArgs[1:]...)
	cmd.Dir = repoPath
	return cmd, nil
}

func splitArgs(args []string) (roleArgs, extraArgs []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
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
