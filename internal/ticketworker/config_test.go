package ticketworker

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigAppliesDefaults(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "version: 1\nworker:\n  command: [go, run, ./cmd/ticket-worker]\n  completion_marker: ZELLIJ_AGENT_WORKER_DONE\n")

	got, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkers != 3 || got.PollInterval != 30*time.Second {
		t.Fatalf("defaults = %d %s", got.MaxWorkers, got.PollInterval)
	}
}

func TestLoadConfigRejectsUnknownFieldAndMultilineMarker(t *testing.T) {
	for name, body := range map[string]string{
		"unknown": "version: 1\nextra: true\nworker:\n  command: [worker]\n  completion_marker: DONE\n",
		"marker":  "version: 1\nworker:\n  command: [worker]\n  completion_marker: 'DONE\\nAGAIN'\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, body)
			if _, err := LoadConfig(root); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadConfigValidatesRequiredValues(t *testing.T) {
	for name, body := range map[string]string{
		"version":                "version: 2\nworker:\n  command: [worker]\n  completion_marker: DONE\n",
		"negative max workers":   "version: 1\nmax_workers: -1\nworker:\n  command: [worker]\n  completion_marker: DONE\n",
		"negative poll interval": "version: 1\npoll_interval: -1s\nworker:\n  command: [worker]\n  completion_marker: DONE\n",
		"empty command":          "version: 1\nworker:\n  command: []\n  completion_marker: DONE\n",
		"blank command element":  "version: 1\nworker:\n  command: [worker, '  ']\n  completion_marker: DONE\n",
		"padded marker":          "version: 1\nworker:\n  command: [worker]\n  completion_marker: ' DONE '\n",
		"carriage return marker": "version: 1\nworker:\n  command: [worker]\n  completion_marker: \"DONE\\rAGAIN\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, body)
			if _, err := LoadConfig(root); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadConfigReadsExplicitValues(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "version: 1\nmax_workers: 5\npoll_interval: 2m\nworker:\n  command: [worker, --flag]\n  completion_marker: DONE\n")

	got, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.MaxWorkers != 5 || got.PollInterval != 2*time.Minute {
		t.Fatalf("config = %+v", got)
	}
	if len(got.Worker.Command) != 2 || got.Worker.Command[1] != "--flag" || got.Worker.CompletionMarker != "DONE" {
		t.Fatalf("worker config = %+v", got.Worker)
	}
}

func TestInitConfigRefusesOverwriteWithoutForce(t *testing.T) {
	root := t.TempDir()
	path, err := InitConfig(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := InitConfig(root, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second init error = %v, want fs.ErrExist", err)
	}
}

func TestInitConfigWritesTemplateAndForceReplaces(t *testing.T) {
	root := t.TempDir()
	path, err := InitConfig(root, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "version: 1\nmax_workers: 3\npoll_interval: 30s\nworker:\n  command: [\"go\", \"run\", \"./cmd/ticket-worker\"]\n  completion_marker: \"ZELLIJ_AGENT_WORKER_DONE\"\n"
	assertFileContents(t, path, want)

	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotPath, err := InitConfig(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path {
		t.Fatalf("path = %q, want %q", gotPath, path)
	}
	assertFileContents(t, path, want)
}

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	path := ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("contents = %q, want %q", got, want)
	}
}
