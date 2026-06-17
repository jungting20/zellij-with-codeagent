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
