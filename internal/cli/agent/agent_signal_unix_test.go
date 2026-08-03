//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package agentcli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const signalHelperTimeout = 5 * time.Second

func TestRunAgentProcessForwardsWrapperSIGTERMAndReturns(t *testing.T) {
	runSignalLifecycleTest(t, syscall.SIGTERM, false)
}

func TestRunAgentProcessReturnsAfterForegroundGroupInterrupt(t *testing.T) {
	runSignalLifecycleTest(t, syscall.SIGINT, true)
}

func runSignalLifecycleTest(t *testing.T, sig syscall.Signal, foregroundGroup bool) {
	t.Helper()
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "child-ready")
	signalPath := filepath.Join(dir, "child-signal")
	returnedPath := filepath.Join(dir, "wrapper-returned")
	outputPath := filepath.Join(dir, "stdio")

	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatalf("create helper output: %v", err)
	}
	t.Cleanup(func() { _ = output.Close() })

	wrapper := exec.Command(
		os.Args[0],
		"-test.run=^TestRunAgentProcessSignalHelper$",
		"--",
		"wrapper",
		dir,
		readyPath,
		signalPath,
		returnedPath,
	)
	wrapper.Stdout = output
	wrapper.Stderr = output
	if foregroundGroup {
		// The test harness makes the wrapper a foreground-group leader. Production
		// deliberately leaves the child in that inherited group.
		wrapper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := wrapper.Start(); err != nil {
		t.Fatalf("start wrapper helper: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- wrapper.Wait() }()
	wrapperFinished := false
	childPID := 0
	t.Cleanup(func() {
		if !wrapperFinished {
			_ = wrapper.Process.Kill()
		}
		if childPID != 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
		if !wrapperFinished {
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	})

	ready := waitForHelperFile(t, readyPath, "child signal readiness")
	fields := strings.Fields(string(ready))
	if len(fields) != 3 {
		t.Fatalf("child readiness = %q, want pid, process group, and cwd", ready)
	}
	childPID, err = strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("parse child pid %q: %v", fields[0], err)
	}
	childProcessGroup, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("parse child process group %q: %v", fields[1], err)
	}
	if fields[2] != dir {
		t.Fatalf("child cwd = %q, want %q", fields[2], dir)
	}

	if foregroundGroup {
		if childProcessGroup != wrapper.Process.Pid {
			t.Fatalf("child process group = %d, want inherited wrapper group %d", childProcessGroup, wrapper.Process.Pid)
		}
		if err := syscall.Kill(-wrapper.Process.Pid, sig); err != nil {
			t.Fatalf("interrupt foreground process group: %v", err)
		}
	} else if err := wrapper.Process.Signal(sig); err != nil {
		t.Fatalf("signal wrapper helper: %v", err)
	}

	select {
	case err := <-done:
		wrapperFinished = true
		if err != nil {
			t.Fatalf("wrapper helper exit: %v; output=%q", err, readHelperOutput(outputPath))
		}
		childPID = 0
	case <-time.After(signalHelperTimeout):
		t.Fatalf("wrapper helper did not return after %s; output=%q", sig, readHelperOutput(outputPath))
	}

	if got := strings.TrimSpace(string(waitForHelperFile(t, signalPath, "forwarded child signal"))); got != sig.String() {
		t.Fatalf("child signal = %q, want %q", got, sig.String())
	}
	if got := strings.TrimSpace(string(waitForHelperFile(t, returnedPath, "wrapper return"))); got != "ok" {
		t.Fatalf("wrapper result = %q, want ok", got)
	}
	stdio := readHelperOutput(outputPath)
	if !strings.Contains(stdio, "child stdout ready") || !strings.Contains(stdio, "child stderr ready") {
		t.Fatalf("child stdio was not preserved: %q", stdio)
	}
}

func TestRunAgentProcessSignalHelper(t *testing.T) {
	args := signalHelperArgs(os.Args)
	if len(args) == 0 {
		return
	}
	if len(args) != 5 {
		fmt.Fprintf(os.Stderr, "signal helper args = %#v\n", args)
		os.Exit(90)
	}

	mode, dir := args[0], args[1]
	readyPath, signalPath, returnedPath := args[2], args[3], args[4]
	switch mode {
	case "wrapper":
		command := []string{
			os.Args[0],
			"-test.run=^TestRunAgentProcessSignalHelper$",
			"--",
			"child",
			dir,
			readyPath,
			signalPath,
			returnedPath,
		}
		err := runAgentProcess(command, dir, os.Stdin, os.Stdout, os.Stderr)
		result := "ok"
		if err != nil {
			result = err.Error()
		}
		if writeErr := writeHelperFile(returnedPath, []byte(result)); writeErr != nil {
			fmt.Fprintln(os.Stderr, writeErr)
			os.Exit(91)
		}
		if err != nil {
			os.Exit(92)
		}
		os.Exit(0)
	case "child":
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signals)

		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(93)
		}
		ready := fmt.Sprintf("%d %d %s", os.Getpid(), syscall.Getpgrp(), cwd)
		if err := writeHelperFile(readyPath, []byte(ready)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(94)
		}
		fmt.Fprintln(os.Stdout, "child stdout ready")
		fmt.Fprintln(os.Stderr, "child stderr ready")
		sig := <-signals
		if err := writeHelperFile(signalPath, []byte(sig.String())); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(95)
		}
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown signal helper mode %q\n", mode)
		os.Exit(96)
	}
}

func signalHelperArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func waitForHelperFile(t *testing.T, path, description string) []byte {
	t.Helper()
	deadline := time.Now().Add(signalHelperTimeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("wait for %s: %v", description, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeHelperFile(path string, data []byte) error {
	temporary := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func readHelperOutput(path string) string {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err.Error()
	}
	return string(data)
}
