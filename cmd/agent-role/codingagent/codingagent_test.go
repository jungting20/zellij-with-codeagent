package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareRejectsMissingPath(t *testing.T) {
	if _, err := prepare(nil); err == nil {
		t.Fatal("prepare(nil) succeeded, want error")
	}
}

func TestPrepareRejectsPathOutsideRepository(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := prepare([]string{path}); err == nil {
		t.Fatal("prepare(path outside repository) succeeded, want error")
	}
}

func TestPrepareRejectsDirectoryWithoutGit(t *testing.T) {
	if _, err := prepare([]string{t.TempDir()}); err == nil {
		t.Fatal("prepare(directory without .git) succeeded, want error")
	}
}

func TestPrepareBuildsCodexCommandInRepository(t *testing.T) {
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

	cmd, err := prepare([]string{repo})
	if err != nil {
		t.Fatalf("prepare(repo) error = %v", err)
	}
	if cmd.Path != codexPath {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, codexPath)
	}
	if cmd.Dir != repo {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, repo)
	}
}

func TestPrepareBuildsCodexCommandFromFileInsideRepository(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	sourceDir := filepath.Join(repo, "internal", "feature")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, "feature.go")
	if err := os.WriteFile(sourcePath, []byte("package feature\n"), 0644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir)

	cmd, err := prepare([]string{sourcePath})
	if err != nil {
		t.Fatalf("prepare(file inside repo) error = %v", err)
	}
	if cmd.Path != codexPath {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, codexPath)
	}
	if cmd.Dir != repo {
		t.Fatalf("cmd.Dir = %q, want repo root %q", cmd.Dir, repo)
	}
}
