package agentworktreemerge

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptValidatesWorktrees(t *testing.T) {
	repo := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	git(repo, "init", "-b", "main")
	git(repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init")
	child := filepath.Join(t.TempDir(), "child")
	git(repo, "worktree", "add", "-b", "feat/child", child)
	prompt, err := Prompt(context.Background(), repo, child)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main", "feat/child", child, "미커밋", "테스트", "유지"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q: %s", want, prompt)
		}
	}
	if _, err := Prompt(context.Background(), repo, repo); err == nil {
		t.Fatal("accepted same worktree")
	}
	other := t.TempDir()
	git(other, "init", "-b", "other")
	git(other, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init")
	if _, err := Prompt(context.Background(), repo, other); err == nil {
		t.Fatal("accepted unrelated repository")
	}
	git(child, "checkout", "--detach")
	if _, err := Prompt(context.Background(), repo, child); err == nil {
		t.Fatal("accepted detached HEAD")
	}
}
