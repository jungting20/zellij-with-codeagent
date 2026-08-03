package agentnext

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPrepareAgentNextCommand(t *testing.T) {
	binDir := t.TempDir()
	fakeBinary := filepath.Join(binDir, "zellij-agent")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake zellij-agent: %v", err)
	}
	t.Setenv("PATH", binDir)

	cmd, err := prepare([]string{"--socket", "/tmp/test.sock"})
	if err != nil {
		t.Fatalf("prepare() error = %v", err)
	}
	want := []string{fakeBinary, "agent", "next", "--socket", "/tmp/test.sock"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, want)
	}
}

func TestRunAgentNextReturnsChildExitCode(t *testing.T) {
	binDir := t.TempDir()
	fakeBinary := filepath.Join(binDir, "zellij-agent")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatalf("write fake zellij-agent: %v", err)
	}
	t.Setenv("PATH", binDir)

	if code := Run(nil); code != 7 {
		t.Fatalf("Run(nil) = %d, want 7", code)
	}
}
