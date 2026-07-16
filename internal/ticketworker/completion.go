package ticketworker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"unicode"
)

const completionOutputLimit = 8 * 1024

var ticketIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

type CompletionRequest struct {
	Command  []string
	TicketID string
	CWD      string
}

type CompletionResult struct {
	Output string
	Err    error
}

type CompletionRunner interface {
	Run(context.Context, CompletionRequest) CompletionResult
}

type ExecCompletionRunner struct{}

func parseCompletionLine(marker, line string) (string, error) {
	prefix := marker + " ticket_id="
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("completion line must match %q", prefix+"<ticket-id>")
	}
	ticketID := strings.TrimPrefix(line, prefix)
	if !ticketIDPattern.MatchString(ticketID) {
		return "", fmt.Errorf("invalid completion ticket ID %q", sanitizeDiagnostic(ticketID))
	}
	return ticketID, nil
}

func (ExecCompletionRunner) Run(ctx context.Context, req CompletionRequest) CompletionResult {
	if len(req.Command) == 0 {
		return CompletionResult{Err: fmt.Errorf("completion command must not be empty")}
	}
	argv := append([]string(nil), req.Command...)
	argv = append(argv, req.TicketID)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = req.CWD

	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return CompletionResult{Output: sanitizeDiagnostic(output.String()), Err: err}
}

type limitedBuffer struct {
	buffer bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	originalLength := len(p)
	remaining := completionOutputLimit - b.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.buffer.Write(p)
	}
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}

func sanitizeDiagnostic(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value))
}
