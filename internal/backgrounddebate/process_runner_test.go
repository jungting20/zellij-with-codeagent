package backgrounddebate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/debaterole"
)

func TestProcessRoleRunnerRunsStructuredRoleCommand(t *testing.T) {
	repository := t.TempDir()
	t.Setenv("ROLE_HELPER_MODE", "valid")
	t.Setenv("ROLE_HELPER_REPOSITORY", repository)

	command := []string{os.Args[0], "-test.run=TestRoleHelperProcess", "--"}
	runner, err := NewProcessRoleRunner(command)
	if err != nil {
		t.Fatalf("NewProcessRoleRunner() error = %v", err)
	}
	command[0] = "changed-after-construction"

	got, err := runner.Run(context.Background(), RoleRequest{
		Role:       Proposer,
		Repository: repository,
		Prompt:     "proposer input",
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := debaterole.Result{
		SchemaVersion: debaterole.SchemaVersion,
		Role:          Proposer.Name,
		Engine:        Proposer.Engine,
		Status:        StatusSuccess,
		Content:       "structured proposal",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Run() = %#v, want %#v", got, want)
	}
}

func TestProcessRoleRunnerRejectsEmptyCommand(t *testing.T) {
	if _, err := NewProcessRoleRunner(nil); err == nil {
		t.Fatal("NewProcessRoleRunner(nil) error = nil, want error")
	}
}

func TestProcessRoleRunnerMapsInvalidResults(t *testing.T) {
	tests := []struct {
		mode string
		kind string
	}{
		{mode: "malformed", kind: FailureMalformed},
		{mode: "trailing", kind: FailureMalformed},
		{mode: "wrong-schema", kind: FailureContract},
		{mode: "wrong-role", kind: FailureContract},
		{mode: "wrong-engine", kind: FailureContract},
		{mode: "failed-status", kind: FailureContract},
		{mode: "empty", kind: FailureEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			repository := t.TempDir()
			t.Setenv("ROLE_HELPER_MODE", tt.mode)
			t.Setenv("ROLE_HELPER_REPOSITORY", repository)
			runner := newHelperProcessRoleRunner(t)

			_, err := runner.Run(context.Background(), RoleRequest{
				Role:       Proposer,
				Repository: repository,
				Prompt:     "proposer input",
				Timeout:    time.Second,
			})
			assertRunError(t, err, tt.kind, nil)
		})
	}
}

func TestProcessRoleRunnerPreservesExitCodeAndBoundsStderr(t *testing.T) {
	repository := t.TempDir()
	t.Setenv("ROLE_HELPER_MODE", "exit-7")
	t.Setenv("ROLE_HELPER_REPOSITORY", repository)
	runner := newHelperProcessRoleRunner(t)

	_, err := runner.Run(context.Background(), RoleRequest{
		Role:       Proposer,
		Repository: repository,
		Prompt:     "proposer input",
		Timeout:    time.Second,
	})
	exitCode := 7
	runErr := assertRunError(t, err, FailureExecution, &exitCode)
	if !strings.Contains(runErr.Message, "stderr-tail-marker") {
		t.Fatalf("RunError.Message = %q, want retained stderr tail", runErr.Message)
	}
	if strings.Contains(runErr.Message, "stderr-head-marker") {
		t.Fatalf("RunError.Message retained discarded stderr head: %q", runErr.Message)
	}
	if len(runErr.Message) > 9*1024 {
		t.Fatalf("len(RunError.Message) = %d, want bounded diagnostic", len(runErr.Message))
	}
}

func TestProcessRoleRunnerIncludesBoundedStderrForOutputErrors(t *testing.T) {
	tests := []struct {
		mode string
		kind string
	}{
		{mode: "malformed-with-stderr", kind: FailureMalformed},
		{mode: "wrong-role-with-stderr", kind: FailureContract},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			repository := t.TempDir()
			t.Setenv("ROLE_HELPER_MODE", tt.mode)
			t.Setenv("ROLE_HELPER_REPOSITORY", repository)

			_, err := newHelperProcessRoleRunner(t).Run(context.Background(), RoleRequest{
				Role: Proposer, Repository: repository, Prompt: "proposer input", Timeout: time.Second,
			})
			runErr := assertRunError(t, err, tt.kind, nil)
			if !strings.Contains(runErr.Message, "stderr-tail-marker") {
				t.Fatalf("RunError.Message = %q, want retained stderr tail", runErr.Message)
			}
			if strings.Contains(runErr.Message, "stderr-head-marker") {
				t.Fatalf("RunError.Message retained discarded stderr head: %q", runErr.Message)
			}
			if len(runErr.Message) > 9*1024 {
				t.Fatalf("len(RunError.Message) = %d, want bounded diagnostic", len(runErr.Message))
			}
		})
	}
}

func TestProcessRoleRunnerTimesOut(t *testing.T) {
	repository := t.TempDir()
	t.Setenv("ROLE_HELPER_MODE", "sleep")
	t.Setenv("ROLE_HELPER_REPOSITORY", repository)
	runner := newHelperProcessRoleRunner(t)

	started := time.Now()
	_, err := runner.Run(context.Background(), RoleRequest{
		Role:       Proposer,
		Repository: repository,
		Prompt:     "proposer input",
		Timeout:    30 * time.Millisecond,
	})
	assertRunError(t, err, FailureTimeout, nil)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Run() elapsed = %v, want prompt timeout", elapsed)
	}
}

func newHelperProcessRoleRunner(t *testing.T) *ProcessRoleRunner {
	t.Helper()
	runner, err := NewProcessRoleRunner([]string{os.Args[0], "-test.run=TestRoleHelperProcess", "--"})
	if err != nil {
		t.Fatalf("NewProcessRoleRunner() error = %v", err)
	}
	return runner
}

func assertRunError(t *testing.T, err error, kind string, exitCode *int) *RunError {
	t.Helper()
	runErr, ok := err.(*RunError)
	if !ok {
		t.Fatalf("Run() error = %T %v, want *RunError", err, err)
	}
	if runErr.Kind != kind {
		t.Fatalf("RunError.Kind = %q, want %q", runErr.Kind, kind)
	}
	if !reflect.DeepEqual(runErr.ExitCode, exitCode) {
		t.Fatalf("RunError.ExitCode = %v, want %v", runErr.ExitCode, exitCode)
	}
	return runErr
}

func TestRoleHelperProcess(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == -1 {
		return
	}

	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	wantArgs := []string{"role", Proposer.Name, "--output-format", "json", os.Getenv("ROLE_HELPER_REPOSITORY")}
	if got := os.Args[separator+1:]; !reflect.DeepEqual(got, wantArgs) {
		fmt.Fprintf(os.Stderr, "args = %q, want %q\n", got, wantArgs)
		os.Exit(4)
	}
	if got := string(stdin); got != "proposer input" {
		fmt.Fprintf(os.Stderr, "stdin = %q, want %q\n", got, "proposer input")
		os.Exit(5)
	}

	result := debaterole.Result{
		SchemaVersion: debaterole.SchemaVersion,
		Role:          Proposer.Name,
		Engine:        Proposer.Engine,
		Status:        StatusSuccess,
		Content:       "structured proposal",
	}
	switch os.Getenv("ROLE_HELPER_MODE") {
	case "valid":
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case "malformed":
		fmt.Fprint(os.Stdout, "not-json")
	case "malformed-with-stderr":
		fmt.Fprint(os.Stderr, "stderr-head-marker"+strings.Repeat("x", 9*1024)+"stderr-tail-marker")
		fmt.Fprint(os.Stdout, "not-json")
	case "trailing":
		_ = json.NewEncoder(os.Stdout).Encode(result)
		fmt.Fprint(os.Stdout, `{"unexpected":true}`)
	case "wrong-schema":
		result.SchemaVersion = "debate-role/v0"
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case "wrong-role":
		result.Role = Critic.Name
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case "wrong-role-with-stderr":
		fmt.Fprint(os.Stderr, "stderr-head-marker"+strings.Repeat("x", 9*1024)+"stderr-tail-marker")
		result.Role = Critic.Name
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case "wrong-engine":
		result.Engine = Critic.Engine
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case "failed-status":
		result.Status = StatusFailed
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case "empty":
		result.Content = " \r\n\t"
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case "exit-7":
		fmt.Fprint(os.Stderr, "stderr-head-marker"+strings.Repeat("x", 9*1024)+"stderr-tail-marker")
		os.Exit(7)
	case "sleep":
		time.Sleep(5 * time.Second)
	case "spawn-grandchild":
		grandchild := exec.Command(os.Args[0], "-test.run=TestRoleGrandchildHelperProcess", "--")
		grandchild.Env = append(os.Environ(), "ROLE_GRANDCHILD_HELPER=1")
		if err := grandchild.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(8)
		}
		if err := os.WriteFile(os.Getenv("ROLE_GRANDCHILD_PID_FILE"), []byte(fmt.Sprint(grandchild.Process.Pid)), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(9)
		}
		time.Sleep(5 * time.Second)
	default:
		fmt.Fprintf(os.Stderr, "unknown ROLE_HELPER_MODE %q\n", os.Getenv("ROLE_HELPER_MODE"))
		os.Exit(6)
	}
	os.Exit(0)
}

func TestRoleGrandchildHelperProcess(t *testing.T) {
	if os.Getenv("ROLE_GRANDCHILD_HELPER") != "1" {
		return
	}
	time.Sleep(5 * time.Second)
	os.Exit(0)
}
