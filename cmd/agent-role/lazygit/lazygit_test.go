package lazygit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommandUsesExactDirectory(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lazygit"), []byte("#!/bin/sh\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dir := filepath.Join(t.TempDir(), "repo with spaces")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	cmd, err := Command(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != dir || len(cmd.Args) != 1 {
		t.Fatalf("%+v", cmd)
	}
	if code := Run([]string{dir}); code != 7 {
		t.Fatalf("exit=%d", code)
	}
	if _, err := Command(""); err == nil {
		t.Fatal("accepted empty cwd")
	}
	if _, err := Command(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("accepted missing cwd")
	}
	if code := Run(nil); code == 0 {
		t.Fatal("accepted missing argument")
	}
}
