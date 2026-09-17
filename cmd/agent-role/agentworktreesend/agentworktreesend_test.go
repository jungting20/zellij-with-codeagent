package agentworktreesend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"zellij-with-codeagent/internal/transport"
)

func TestExecuteAllWorktreesAndContinueAfterFailure(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	root := t.TempDir()
	repo := filepath.Join(root, "main")
	child := filepath.Join(root, "child space")
	for _, args := range [][]string{{"init", repo}, {"-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init"}, {"-C", repo, "worktree", "add", "-b", "child", child}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	results, err := Execute(context.Background(), child, `printf 'done' > marker; pwd; test "${PWD##*/}" != main`)
	if err != nil || len(results) != 2 {
		t.Fatalf("%+v %v", results, err)
	}
	if results[0].Error == "" || results[1].Error != "" || !strings.Contains(results[1].Output, "child space") {
		t.Fatalf("%+v", results)
	}
	for _, p := range []string{repo, child} {
		if out, err := os.ReadFile(filepath.Join(p, "marker")); err != nil || string(out) != "done" {
			t.Fatalf("%s %v", out, err)
		}
	}
	if _, err := Execute(context.Background(), repo, " "); err == nil {
		t.Fatal("empty command accepted")
	}
	if _, err := Execute(context.Background(), root, "pwd"); err == nil {
		t.Fatal("non-repository accepted")
	}
}

type childLister struct {
	rows []transport.AgentWithPane
	err  error
}

func (c childLister) ListAgents(context.Context) (transport.ListAgentsResponse, error) {
	return transport.ListAgentsResponse{Agents: c.rows}, c.err
}
func TestExecuteChildrenRestrictsScope(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	root := t.TempDir()
	paths := map[string]string{}
	for _, name := range []string{"parent", "child", "other", "grandchild", "closed"} {
		paths[name] = filepath.Join(root, name)
		if err := os.Mkdir(paths[name], 0700); err != nil {
			t.Fatal(err)
		}
	}
	row := func(id, parent, path, status string) transport.AgentWithPane {
		return transport.AgentWithPane{Agent: transport.Agent{ID: id}, Pane: transport.Pane{ID: id, ParentPaneID: parent, CWD: path, Status: status}}
	}
	client := childLister{rows: []transport.AgentWithPane{
		row("parent", "", paths["parent"], "running"),
		row("child", "parent", paths["child"], "running"),
		row("duplicate", "parent", paths["child"], "running"),
		row("same-directory", "parent", paths["parent"], "running"),
		row("other", "another-parent", paths["other"], "running"),
		row("grandchild", "child", paths["grandchild"], "running"),
		row("closed", "parent", paths["closed"], "closed"),
	}}
	results, err := ExecuteChildren(context.Background(), client, "parent", "printf x >> marker")
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	for name, path := range paths {
		out, err := os.ReadFile(filepath.Join(path, "marker"))
		if name == "child" {
			if err != nil || string(out) != "x" {
				t.Fatalf("child: %s %v", out, err)
			}
		} else if !os.IsNotExist(err) {
			t.Fatalf("executed in %s", name)
		}
	}
	for _, id := range []string{"missing", "other"} {
		if _, err := ExecuteChildren(context.Background(), client, id, "printf x >> marker"); err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
}
