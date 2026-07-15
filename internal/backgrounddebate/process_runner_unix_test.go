//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package backgrounddebate

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessRoleRunnerCancellationTerminatesGrandchild(t *testing.T) {
	repository := t.TempDir()
	pidFile := repository + "/grandchild.pid"
	t.Setenv("ROLE_HELPER_MODE", "spawn-grandchild")
	t.Setenv("ROLE_HELPER_REPOSITORY", repository)
	t.Setenv("ROLE_GRANDCHILD_PID_FILE", pidFile)

	_, err := newHelperProcessRoleRunner(t).Run(context.Background(), RoleRequest{
		Role: Proposer, Repository: repository, Prompt: "proposer input", Timeout: 150 * time.Millisecond,
	})
	assertRunError(t, err, FailureTimeout, nil)

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read grandchild pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse grandchild pid %q: %v", data, err)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild process %d remained alive after role timeout", pid)
}
