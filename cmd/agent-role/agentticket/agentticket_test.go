package agentticket

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandUsesSelectedProjectAndLiteralPrompt(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "zellij-agent"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	prompt := "한글 작업\n$(touch should-not-exist) `literal`"
	cmd, err := Command(context.Background(), Request{Action: "add", Directory: t.TempDir(), Agent: "claude", Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Args, "\x00")
	for _, want := range []string{"ticket-worker\x00add", "--prompt\x00" + prompt, "--agent\x00claude", "--worktree-branch\x00ticket/"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args = %q", cmd.Args)
		}
	}
	if strings.Contains(joined, "--worktree\x00") {
		t.Fatal("unexpected isolation")
	}
	cmd, err = Command(context.Background(), Request{Action: "start", Directory: cmd.Dir, Session: "selected-session", Socket: "/tmp/custom.sock", Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cmd.Args, " "); !strings.Contains(got, "--zellij-session selected-session --socket /tmp/custom.sock --timeout 3s") {
		t.Fatal(got)
	}
}

func TestCommandValidation(t *testing.T) {
	for _, req := range []Request{{Action: "init", Directory: "/tmp"}, {Action: "add", Directory: "/tmp", Prompt: " "}, {Action: "start", Directory: "/tmp"}, {Action: "list"}} {
		if _, err := Command(context.Background(), req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
}

func TestStartPassesDefaultAgent(t *testing.T) {
	cmd, err := Command(context.Background(), Request{Action: "start", Directory: "/tmp", Session: "s", DefaultAgent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cmd.Args, " "), "--default-agent claude") {
		t.Fatal(cmd.Args)
	}
}
