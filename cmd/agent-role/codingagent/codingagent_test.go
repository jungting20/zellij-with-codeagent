package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPrepareRejectsMissingPath(t *testing.T) {
	if _, err := prepare(nil); err == nil {
		t.Fatal("prepare(nil) succeeded, want error")
	}
}

func TestPrepareRejectsInvalidArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown flag": {"--unknown", "/repo"},
		"yolo only":    {"--yolo"},
		"extra path":   {"/repo", "/other"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prepare(args); err == nil {
				t.Fatalf("prepare(%q) succeeded, want error", args)
			}
		})
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
	if !slices.Equal(cmd.Args, []string{codexPath}) {
		t.Fatalf("cmd.Args = %#v, want plain Codex command", cmd.Args)
	}
}

func TestPrepareBuildsYoloCodexCommandInRepository(t *testing.T) {
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

	cmd, err := prepare([]string{"--yolo", repo})
	if err != nil {
		t.Fatalf("prepare(--yolo repo) error = %v", err)
	}
	wantArgs := []string{codexPath, "--dangerously-bypass-approvals-and-sandbox"}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, wantArgs)
	}
	if cmd.Dir != repo {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, repo)
	}
}

func TestPrepareBuildsSelectedAgentCommands(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	binDir := t.TempDir()
	paths := map[string]string{}
	for _, executable := range []string{"codex", "claude", "agy", "agent"} {
		path := filepath.Join(binDir, executable)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", executable, err)
		}
		paths[executable] = path
	}
	t.Setenv("PATH", binDir)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "claude",
			args: []string{"--agent", "claude", "--yolo", repo},
			want: []string{paths["claude"], "--dangerously-skip-permissions"},
		},
		{
			name: "gemini with extra arguments",
			args: []string{"--agent", "gemini", "--yolo", repo, "--", "--model", "gemini-3"},
			want: []string{paths["agy"], "--dangerously-skip-permissions", "--model", "gemini-3"},
		},
		{
			name: "cursor",
			args: []string{"--agent", "cursor", "--yolo", repo},
			want: []string{paths["agent"], "--yolo", "--trust"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := prepare(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(cmd.Args, tt.want) {
				t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, tt.want)
			}
			if cmd.Dir != repo {
				t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, repo)
			}
		})
	}
}

func TestPrepareRejectsInvalidAgentKindsAndPaths(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown agent":       {"--agent", "unknown", "/repo"},
		"executable not kind": {"--agent", "agy", "/repo"},
		"missing agent path":  {"--agent", "gemini"},
		"multiple paths":      {"--agent", "gemini", "/repo", "/other"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prepare(args); err == nil {
				t.Fatalf("prepare(%q) succeeded, want error", args)
			}
		})
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
