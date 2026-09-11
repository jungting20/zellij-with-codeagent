package prompteditor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommandAndExitCode(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nvim"), []byte("#!/bin/sh\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	path := filepath.Join(t.TempDir(), "prompt with spaces.md")
	cmd, err := Command(path)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Args[len(cmd.Args)-1] != path || cmd.Args[len(cmd.Args)-2] != "--" {
		t.Fatalf("args=%q", cmd.Args)
	}
	if code := Run([]string{path}); code != 7 {
		t.Fatalf("exit=%d", code)
	}
	if code := Run(nil); code != 2 {
		t.Fatalf("usage exit=%d", code)
	}
	if _, err := Command(""); err == nil {
		t.Fatal("accepted empty file")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := Command(path); err == nil {
		t.Fatal("accepted missing nvim")
	}
}
