package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRequiresSocket(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "requires --socket") {
		t.Fatalf("stderr = %q, want socket error", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2 for flag help", code)
	}
	if !strings.Contains(stderr.String(), "Usage of fake-planner") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}
