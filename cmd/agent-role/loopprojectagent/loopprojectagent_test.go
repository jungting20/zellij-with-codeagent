package loopprojectagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareBuildsCodexCommandsForLoopProjectRoles(t *testing.T) {
	repo := newGitRepository(t)
	runnerSkill := newRunnerSkill(t)
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "zellij-agent"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir)

	for _, tt := range []struct {
		name      string
		mode      Mode
		access    string
		role      string
		forbidden bool
	}{
		{name: "worker", mode: ModeWorker, access: "full", role: "loop-project-worker"},
		{name: "verifier", mode: ModeVerifier, access: "read-only", role: "loop-project-verifier", forbidden: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := prepare(tt.mode, []string{
				"--repository", repo,
				"--runner-skill", runnerSkill,
				"--orchestrator-pane", "orchestrator-1",
			})
			if err != nil {
				t.Fatalf("prepare() error = %v", err)
			}

			bootstrap := bootstrapPrompt(tt.mode, repo, runnerSkill, "orchestrator-1")
			want := []string{
				"zellij-agent", "agent", "start", "codex",
				"--cwd", repo,
				"--access", tt.access,
				"--", bootstrap,
			}
			if got := cmd.Args; !sameStrings(got, want) {
				t.Fatalf("command = %#v, want %#v", got, want)
			}
			for _, wantText := range []string{tt.role, repo, runnerSkill, "orchestrator-1", "do not write", "ctl message"} {
				if !strings.Contains(bootstrap, wantText) {
					t.Fatalf("bootstrap = %q, missing %q", bootstrap, wantText)
				}
			}
			if tt.forbidden && !strings.Contains(bootstrap, "code_changes: FORBIDDEN") {
				t.Fatalf("verifier bootstrap = %q, missing code_changes restriction", bootstrap)
			}
		})
	}
}

func TestPrepareRequiresValidLoopProjectPaths(t *testing.T) {
	repo := newGitRepository(t)
	runnerSkill := newRunnerSkill(t)

	for _, args := range [][]string{
		{"--repository", repo, "--runner-skill", runnerSkill},
		{"--repository", repo, "--orchestrator-pane", "orchestrator-1"},
		{"--runner-skill", runnerSkill, "--orchestrator-pane", "orchestrator-1"},
		{"--repository", t.TempDir(), "--runner-skill", runnerSkill, "--orchestrator-pane", "orchestrator-1"},
		{"--repository", repo, "--runner-skill", t.TempDir(), "--orchestrator-pane", "orchestrator-1"},
	} {
		if _, err := prepare(ModeWorker, args); err == nil {
			t.Fatalf("prepare(%#v) error = nil, want validation error", args)
		}
	}
}

func TestRunPreservesChildExitCode(t *testing.T) {
	repo := newGitRepository(t)
	runnerSkill := newRunnerSkill(t)
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "zellij-agent"), "#!/bin/sh\nexit 23\n")
	t.Setenv("PATH", binDir)

	if got := RunWorker([]string{
		"--repository", repo,
		"--runner-skill", runnerSkill,
		"--orchestrator-pane", "orchestrator-1",
	}); got != 23 {
		t.Fatalf("RunWorker() = %d, want child exit code 23", got)
	}
}

func newGitRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", repo)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return repo
}

func newRunnerSkill(t *testing.T) string {
	t.Helper()
	runnerSkill := t.TempDir()
	if err := os.WriteFile(filepath.Join(runnerSkill, "SKILL.md"), []byte("# Runner skill\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return runnerSkill
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
