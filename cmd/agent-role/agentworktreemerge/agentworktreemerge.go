// Package agentworktreemerge prepares and sends worktree integration requests.
package agentworktreemerge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func Run(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: agent-worktree-merge <parent-path> <child-path>")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prompt, err := Prompt(ctx, args[0], args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(os.Stdout, prompt)
	return 0
}

func git(ctx context.Context, cwd string, args ...string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", fmt.Errorf("working directory is required")
	}
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspect worktree: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

type checkout struct{ path, branch, common string }

func inspect(ctx context.Context, cwd string) (checkout, error) {
	var c checkout
	var err error
	c.path, err = git(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return c, err
	}
	c.path, err = filepath.EvalSymlinks(c.path)
	if err != nil {
		return c, err
	}
	c.common, err = git(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return c, err
	}
	c.common, err = filepath.EvalSymlinks(c.common)
	if err != nil {
		return c, err
	}
	c.branch, err = git(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return c, fmt.Errorf("worktree must have a checked-out branch: %w", err)
	}
	return c, nil
}

// Prompt reads Git metadata without modifying either working tree.
func Prompt(ctx context.Context, parentPath, childPath string) (string, error) {
	parent, err := inspect(ctx, parentPath)
	if err != nil {
		return "", err
	}
	child, err := inspect(ctx, childPath)
	if err != nil {
		return "", err
	}
	if parent.common != child.common || parent.path == child.path || parent.branch == child.branch {
		return "", fmt.Errorf("select a distinct child worktree in the same repository")
	}
	return fmt.Sprintf("자식 worktree %q의 브랜치 %q를 부모 저장소 %q의 브랜치 %q에 병합해줘. 경로와 브랜치가 요청 시점과 같은지, 자식 작업이 끝났는지 먼저 확인해줘. 양쪽 작업 상태와 변경 내용을 확인하고, 미커밋 변경이 있으면 임의로 포함하지 말고 알려줘. 충돌은 작업 의도를 확인해 해결하고, 의도가 불명확하면 물어봐줘. 관련 테스트 후 결과를 보고해줘. worktree와 브랜치는 유지해줘.", child.path, child.branch, parent.path, parent.branch), nil
}

type Client interface {
	ListAgents(context.Context) (transport.ListAgentsResponse, error)
	SendInput(context.Context, string, transport.SendInputRequest) error
}

// Request resolves agent identities again immediately before preparing the input.
func Request(ctx context.Context, client Client, parentID, childID string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := client.ListAgents(ctx)
	if err != nil {
		return err
	}
	var parent, child transport.AgentWithPane
	for _, row := range list.Agents {
		if row.Agent.ID == parentID {
			parent = row
		}
		if row.Agent.ID == childID {
			child = row
		}
	}
	if parent.Pane.ID == "" || child.Pane.ID == "" || child.Pane.ParentPaneID != parent.Pane.ID {
		return fmt.Errorf("parent/child agent relationship is no longer available")
	}
	if parent.Pane.Status != "running" || child.Pane.Status != "running" || parent.Agent.State != "idle" || child.Agent.State != "idle" {
		return fmt.Errorf("부모와 자식 agent가 모두 idle일 때 병합을 요청할 수 있습니다")
	}
	prompt, err := Prompt(ctx, parent.Pane.CWD, child.Pane.CWD)
	if err != nil {
		return err
	}
	return client.SendInput(ctx, parent.Pane.ID, transport.SendInputRequest{Text: prompt + "\n"})
}
