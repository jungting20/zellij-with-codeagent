package prompteditor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Command opens a prompt file in Neovim without invoking a shell.
func Command(path string) (*exec.Cmd, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("prompt file is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("nvim", "-n", "-i", "NONE", "-c", "setlocal filetype=markdown", "--", path)
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	return cmd, nil
}

func Run(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: prompt-editor <file>")
		return 2
	}
	cmd, err := Command(args[0])
	if err == nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		err = cmd.Run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "prompt-editor:", err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}
