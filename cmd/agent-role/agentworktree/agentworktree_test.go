package agentworktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCreate(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	git("init")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init")
	root := t.TempDir()
	now := time.Date(2026, 9, 12, 12, 34, 56, 0, time.Local)
	path, err := create(context.Background(), repo, root, now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "project-123456" {
		t.Fatal(path)
	}
	out, err := exec.Command("git", "-C", path, "branch", "--show-current").Output()
	if err != nil || string(out) != "project-123456\n" {
		t.Fatalf("%s %v", out, err)
	}
	namedPath, err := createNamed(context.Background(), repo, root, "feat/search", now)
	if err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command("git", "-C", namedPath, "branch", "--show-current").Output()
	if err != nil || string(out) != "feat/search-123456\n" || filepath.Dir(namedPath) != root {
		t.Fatalf("path=%s branch=%s err=%v", namedPath, out, err)
	}
	for _, name := range []string{"../escape", "bad name", "-option", "bad..ref", "@{-1}"} {
		if _, err := createNamed(context.Background(), repo, root, name, now); err == nil {
			t.Fatalf("accepted invalid branch %q", name)
		}
	}
	if _, err := CreateNamed(context.Background(), repo, "  "); err == nil {
		t.Fatal("accepted empty branch")
	}
	if _, err := create(context.Background(), repo, root, now); err == nil {
		t.Fatal("expected collision error")
	}
	if _, err := create(context.Background(), t.TempDir(), root, now); err == nil {
		t.Fatal("expected non-repository error")
	}
}
