package listselectorcli

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type resultModel struct {
	err error
}

func (m resultModel) Init() tea.Cmd                       { return nil }
func (m resultModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m resultModel) View() string                        { return "" }
func (m resultModel) ResultError() error                  { return m.err }

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	exitCode := Run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr, Config{
		RunProgram: func(tea.Model, io.Reader, io.Writer) (tea.Model, error) {
			called = true
			return nil, nil
		},
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if called {
		t.Fatal("program runner called for help")
	}
	if !strings.Contains(stdout.String(), "Usage: zellij-agent list-selector") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunRejectsArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"unexpected"}, strings.NewReader(""), &stdout, &stderr, Config{})

	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "does not accept arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunProgramFailure(t *testing.T) {
	want := errors.New("terminal failed")
	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader(""), &stdout, &stderr, Config{
		NewModel: func() tea.Model { return resultModel{} },
		RunProgram: func(tea.Model, io.Reader, io.Writer) (tea.Model, error) {
			return nil, want
		},
	})

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunSuccessForwardsStreams(t *testing.T) {
	stdin := strings.NewReader("input")
	var stdout, stderr bytes.Buffer
	wantModel := resultModel{}
	exitCode := Run(nil, stdin, &stdout, &stderr, Config{
		NewModel: func() tea.Model { return wantModel },
		RunProgram: func(model tea.Model, gotStdin io.Reader, gotStdout io.Writer) (tea.Model, error) {
			if model != wantModel {
				t.Fatalf("model = %#v, want %#v", model, wantModel)
			}
			if gotStdin != stdin {
				t.Fatal("stdin was not forwarded")
			}
			if gotStdout != &stdout {
				t.Fatal("stdout was not forwarded")
			}
			return model, nil
		},
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunReturnsChildExitCode(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 7").Run()
	if err == nil {
		t.Fatal("child command unexpectedly succeeded")
	}

	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader(""), &stdout, &stderr, Config{
		NewModel: func() tea.Model { return resultModel{} },
		RunProgram: func(tea.Model, io.Reader, io.Writer) (tea.Model, error) {
			return resultModel{err: err}, nil
		},
	})

	if exitCode != 7 {
		t.Fatalf("exit code = %d, want 7", exitCode)
	}
	if !strings.Contains(stderr.String(), "exit status 7") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
