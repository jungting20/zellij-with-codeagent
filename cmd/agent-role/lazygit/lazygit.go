package lazygit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Command prepares lazygit in the supplied directory without invoking a shell.
func Command(path string) (*exec.Cmd, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("working directory is required")
	}
	dir, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", dir)
	}
	cmd := exec.Command("lazygit")
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	cmd.Dir = dir
	return cmd, nil
}

func Run(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: lazygit <path>")
		return 2
	}
	cmd, err := Command(args[0])
	if err == nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		err = cmd.Run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazygit:", err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}
