package rolecli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"zellij-with-codeagent/internal/roles"
)

func TestCodingAgentRoleCatalog(t *testing.T) {
	spec, ok := roles.Lookup(roles.RoleCodingAgent)
	if !ok {
		t.Fatal("coding-agent role missing")
	}
	want := "coding-agent [--agent kind] [--yolo] <path> [-- agent-args...]"
	if spec.Usage != want {
		t.Fatalf("coding-agent usage = %q, want %q", spec.Usage, want)
	}
}

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

func TestRunDispatchesDebateProposer(t *testing.T) {
	repo := newTemporaryGitRepository(t)
	binDir := t.TempDir()
	writeFakeProvider(t, filepath.Join(binDir, "agy"), "#!/bin/sh\nprintf 'proposer answer\\n'\n")
	t.Setenv("PATH", binDir)
	setStdin(t, "proposal prompt\n")

	if code := Run([]string{"debate-proposer", repo}); code != 0 {
		t.Fatalf("Run(debate-proposer) = %d, want 0", code)
	}
}

func TestRunDispatchesDebateCritic(t *testing.T) {
	repo := newTemporaryGitRepository(t)
	binDir := t.TempDir()
	writeFakeProvider(t, filepath.Join(binDir, "agent"), "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"critic answer\"}'\n")
	t.Setenv("PATH", binDir)
	setStdin(t, "critique prompt\n")

	if code := Run([]string{"debate-critic", repo}); code != 0 {
		t.Fatalf("Run(debate-critic) = %d, want 0", code)
	}
}

func TestRunDispatchesDebateJudge(t *testing.T) {
	repo := newTemporaryGitRepository(t)
	binDir := t.TempDir()
	writeFakeProvider(t, filepath.Join(binDir, "codex"), "#!/bin/sh\nprintf 'judge answer\\n'\n")
	t.Setenv("PATH", binDir)
	setStdin(t, "judgment prompt\n")

	if code := Run([]string{"debate-judge", repo}); code != 0 {
		t.Fatalf("Run(debate-judge) = %d, want 0", code)
	}
}

func TestRunDispatchesTabNetworkValidation(t *testing.T) {
	if code := Run([]string{"tab-network", "--port", "0"}); code == 0 {
		t.Fatalf("Run(tab-network --port 0) = %d, want non-zero", code)
	}
}

func TestRunDispatchesTabWatcherValidation(t *testing.T) {
	if code := Run([]string{"tab-watcher", "--port", "0"}); code == 0 {
		t.Fatalf("Run(tab-watcher --port 0) = %d, want non-zero", code)
	}
}

func TestRunDispatchesTicketManagerValidation(t *testing.T) {
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
		_ = r.Close()
	})
	code := Run([]string{"ticket-manager"})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 || !strings.Contains(string(output), "ticket-manager [options] <path>") || strings.Contains(string(output), "unknown role") {
		t.Fatalf("code=%d stderr=%q", code, output)
	}
}

func newTemporaryGitRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", repo)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return repo
}

func writeFakeProvider(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake provider: %v", err)
	}
}

func setStdin(t *testing.T, input string) {
	t.Helper()
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = r.Close()
	})
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
}
