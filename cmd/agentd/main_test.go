package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunWithoutArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "agentd daemon skeleton") {
		t.Fatalf("stdout = %q, want daemon skeleton message", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: agentd") {
		t.Fatalf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "agentd dev") {
		t.Fatalf("stdout = %q, want version", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunInvalidArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--unknown"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown argument: --unknown") {
		t.Fatalf("stderr = %q, want unknown argument error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage: agentd") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestRunServeRequiresSocket(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"serve"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "serve requires --socket") {
		t.Fatalf("stderr = %q, want missing socket error", stderr.String())
	}
}

func TestRunServeCanStopWithCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	socketPath := fmt.Sprintf("/tmp/agentd-test-%d.sock", time.Now().UnixNano())
	defer os.Remove(socketPath)

	code := runContext(ctx, []string{"serve", "--socket", socketPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("runContext() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "agentd serving on unix socket") {
		t.Fatalf("stdout = %q, want serving message", stdout.String())
	}
}
