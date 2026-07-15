package backgrounddebate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"zellij-with-codeagent/internal/debaterole"
)

const maxRoleStderr = 8 * 1024

type ProcessRoleRunner struct {
	command []string
}

func NewProcessRoleRunner(command []string) (*ProcessRoleRunner, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, errors.New("role command is required")
	}
	return &ProcessRoleRunner{command: append([]string(nil), command...)}, nil
}

func (r *ProcessRoleRunner) Run(ctx context.Context, req RoleRequest) (debaterole.Result, error) {
	roleCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		roleCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	args := append(append([]string{}, r.command[1:]...), "role", req.Role.Name, "--output-format", "json", req.Repository)
	cmd := exec.CommandContext(roleCtx, r.command[0], args...)
	configureProcessTreeCancellation(cmd)
	cmd.Dir = req.Repository
	cmd.Stdin = strings.NewReader(req.Prompt)
	var stdout bytes.Buffer
	stderr := &tailBuffer{limit: maxRoleStderr}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if roleCtx.Err() != nil {
			return debaterole.Result{}, withStderrDiagnostic(&RunError{Kind: FailureTimeout, Message: roleCtx.Err().Error()}, stderr)
		}
		var exitErr *exec.ExitError
		var exitCode *int
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			exitCode = &code
		}
		return debaterole.Result{}, withStderrDiagnostic(&RunError{
			Kind: FailureExecution, Message: fmt.Sprintf("role command failed: %v", err), ExitCode: exitCode,
		}, stderr)
	}

	var result debaterole.Result
	decoder := json.NewDecoder(&stdout)
	if err := decoder.Decode(&result); err != nil {
		return debaterole.Result{}, withStderrDiagnostic(malformedRoleOutput(err), stderr)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return debaterole.Result{}, withStderrDiagnostic(malformedRoleOutput(err), stderr)
	}
	if result.SchemaVersion != debaterole.SchemaVersion {
		return debaterole.Result{}, withStderrDiagnostic(contractMismatch(fmt.Sprintf("expected schema %q, got %q", debaterole.SchemaVersion, result.SchemaVersion)), stderr)
	}
	if result.Role != req.Role.Name {
		return debaterole.Result{}, withStderrDiagnostic(contractMismatch(fmt.Sprintf("expected role %q, got %q", req.Role.Name, result.Role)), stderr)
	}
	if result.Engine != req.Role.Engine {
		return debaterole.Result{}, withStderrDiagnostic(contractMismatch(fmt.Sprintf("expected engine %q, got %q", req.Role.Engine, result.Engine)), stderr)
	}
	if result.Status != StatusSuccess {
		return debaterole.Result{}, withStderrDiagnostic(contractMismatch(fmt.Sprintf("expected status %q, got %q", StatusSuccess, result.Status)), stderr)
	}
	if strings.TrimSpace(result.Content) == "" {
		return debaterole.Result{}, withStderrDiagnostic(&RunError{Kind: FailureEmpty, Message: "role returned empty content"}, stderr)
	}
	return result, nil
}

func malformedRoleOutput(err error) *RunError {
	return &RunError{Kind: FailureMalformed, Message: fmt.Sprintf("decode role output: %v", err)}
}

func contractMismatch(message string) *RunError {
	return &RunError{Kind: FailureContract, Message: message}
}

func withStderrDiagnostic(runErr *RunError, stderr *tailBuffer) *RunError {
	diagnostic := strings.TrimSpace(stderr.String())
	if diagnostic != "" {
		runErr.Message += ": " + diagnostic
	}
	return runErr
}

type tailBuffer struct {
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return written, nil
	}
	overflow := len(b.data) + len(p) - b.limit
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *tailBuffer) String() string {
	return string(b.data)
}
