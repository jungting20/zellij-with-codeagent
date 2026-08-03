package listselectorcli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/listselector"
)

// ProgramRunner runs a selector model with the supplied terminal streams.
type ProgramRunner func(tea.Model, io.Reader, io.Writer) (tea.Model, error)

// Config provides testable selector construction and execution hooks.
type Config struct {
	NewModel   func() tea.Model
	RunProgram ProgramRunner
}

// Run validates CLI arguments, runs the selector, and returns a process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg Config) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		printUsage(stdout)
		return 0
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "list-selector does not accept arguments")
		return 2
	}

	newModel := cfg.NewModel
	if newModel == nil {
		newModel = func() tea.Model { return listselector.NewModel() }
	}
	runner := cfg.RunProgram
	if runner == nil {
		runner = runProgram
	}

	finalModel, err := runner(newModel(), stdin, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "list-selector failed: %v\n", err)
		return 1
	}
	if err := listselector.ResultError(finalModel); err != nil {
		fmt.Fprintln(stderr, err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

func runProgram(model tea.Model, stdin io.Reader, stdout io.Writer) (tea.Model, error) {
	return tea.NewProgram(
		model,
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
	).Run()
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent list-selector")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Select and start a coding agent in the current terminal.")
}
