package rolecli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunDispatchesCodingAgentRole(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir)

	if code := Run([]string{"coding-agent", repo}); code != 0 {
		t.Fatalf("Run(coding-agent) = %d, want 0", code)
	}
}

func TestRunDispatchesDebateCoordinator(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\n/bin/cat >/dev/null\nprintf 'ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	input := "<<<DEBATE_SYNTHESIS_BEGIN>>>\nCompletion-Marker: <<<DONE>>>\nTopic: x\n<<<DEBATE_SYNTHESIS_END>>>\n"
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	t.Cleanup(func() { os.Stdin = oldStdin })
	os.Stdin = r
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if code := Run([]string{"debate-coordinator", repo}); code != 0 {
		t.Fatalf("Run(debate-coordinator) = %d, want 0", code)
	}
}
