package ticketworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCompletionLine(t *testing.T) {
	for _, ticketID := range []string{"TICKET-123", "ABC_1", "team.issue:42", "a"} {
		t.Run(ticketID, func(t *testing.T) {
			got, err := parseCompletionLine("DONE", "DONE ticket_id="+ticketID)
			if err != nil {
				t.Fatalf("parseCompletionLine() error = %v", err)
			}
			if got != ticketID {
				t.Fatalf("ticket ID = %q, want %q", got, ticketID)
			}
		})
	}
}

func TestParseCompletionLineRejectsMalformedInput(t *testing.T) {
	for name, line := range map[string]string{
		"missing suffix": "DONE",
		"empty id":       "DONE ticket_id=",
		"whitespace":     "DONE ticket_id=TICKET 123",
		"extra token":    "DONE ticket_id=TICKET-123 extra",
		"wrong marker":   "OTHER ticket_id=TICKET-123",
		"control":        "DONE ticket_id=TICKET\t123",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCompletionLine("DONE", line); err == nil {
				t.Fatalf("parseCompletionLine(%q) error = nil", line)
			}
		})
	}
}

func TestExecCompletionRunnerRunsArgvFromCWD(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "fake-ticket", "#!/bin/sh\nprintf 'cwd=%s args=%s\\n' \"$PWD\" \"$*\"\nprintf 'stderr-line\\n' >&2\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := (ExecCompletionRunner{}).Run(context.Background(), CompletionRequest{
		Command:  []string{"fake-ticket", "complete"},
		TicketID: "TICKET-123",
		CWD:      dir,
	})

	if result.Err != nil {
		t.Fatalf("Run() error = %v", result.Err)
	}
	for _, want := range []string{"cwd=" + dir, "args=complete TICKET-123", "stderr-line"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("output = %q, want %q", result.Output, want)
		}
	}
}

func TestExecCompletionRunnerReportsExitAndBoundsOutput(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "fake-ticket", "#!/bin/sh\nhead -c 10000 /dev/zero | tr '\\0' x\nexit 7\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := (ExecCompletionRunner{}).Run(context.Background(), CompletionRequest{
		Command: []string{"fake-ticket"}, TicketID: "TICKET-123", CWD: dir,
	})

	if result.Err == nil {
		t.Fatal("Run() error = nil, want exit error")
	}
	if len(result.Output) > completionOutputLimit {
		t.Fatalf("output length = %d, want <= %d", len(result.Output), completionOutputLimit)
	}
}

func TestExecCompletionRunnerHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "fake-ticket", "#!/bin/sh\nsleep 5\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	result := (ExecCompletionRunner{}).Run(ctx, CompletionRequest{
		Command: []string{"fake-ticket"}, TicketID: "TICKET-123", CWD: dir,
	})

	if result.Err == nil || (!errors.Is(result.Err, context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		t.Fatalf("Run() error = %v, context error = %v", result.Err, ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func writeExecutable(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
