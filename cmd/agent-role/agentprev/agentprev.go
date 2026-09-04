package agentprev

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
		fmt.Fprintf(os.Stderr, "Error running zellij-agent: %v\n", err)
		return 1
	}
	return 0
}

func prepare(args []string) (*exec.Cmd, error) {
	binary, err := exec.LookPath("zellij-agent")
	if err != nil {
		return nil, fmt.Errorf("zellij-agent executable not found on PATH")
	}
	return exec.Command(binary, append([]string{"agent", "prev"}, args...)...), nil
}
