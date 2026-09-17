package transport

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDaemonLockSurvivesMissingSocketAndReleasesAfterCrash(t *testing.T) {
	if path := os.Getenv("TEST_DAEMON_LOCK_SOCKET"); path != "" {
		lock, err := AcquireDaemonLock(path)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		fmt.Println("locked")
		// Wait for the parent to kill us, without normal deferred cleanup.
		_, _ = bufio.NewReader(os.Stdin).ReadByte()
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.sock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonLockSurvivesMissingSocketAndReleasesAfterCrash$")
	cmd.Env = append(os.Environ(), "TEST_DAEMON_LOCK_SOCKET="+path)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("child readiness = %q, %v", line, err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, filepath.Join(alias, "daemon.sock")} {
		if lock, err := AcquireDaemonLock(candidate); err == nil {
			lock.Close()
			t.Fatalf("duplicate acquired lock for %s without a socket", candidate)
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lock, err := AcquireDaemonLock(path)
	if err != nil {
		t.Fatalf("restart after crash: %v", err)
	}
	lock.Close()
}
