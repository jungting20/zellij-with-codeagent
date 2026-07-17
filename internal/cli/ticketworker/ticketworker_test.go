package ticketworkercli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelpDescribesPlaceholder(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0", code)
	}
	for _, want := range []string{"Usage: zellij-agent ticket-worker", "not implemented"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "init") || strings.Contains(stdout.String(), "start") {
		t.Fatalf("stdout = %q, want no former subcommands", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunBareInvocationIsUnavailable(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run(nil, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run() exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "ticket-worker is not implemented") {
		t.Fatalf("stdout=%q stderr=%q, want unavailable message on stderr", stdout.String(), stderr.String())
	}
}

func TestRunFormerSubcommandIsUnavailable(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"init"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run() exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "ticket-worker is not implemented") {
		t.Fatalf("stdout=%q stderr=%q, want unavailable message on stderr", stdout.String(), stderr.String())
	}
}
