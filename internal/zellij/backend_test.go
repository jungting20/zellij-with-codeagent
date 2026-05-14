package zellij

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCreatePaneParsesReturnedPaneID(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "terminal_5\n"}},
		},
	}
	backend := NewBackend(Options{
		Session: "agent-session",
		Runner:  runner,
	})

	id, err := backend.CreatePane(context.Background(), CreatePaneRequest{
		Name:    "tests",
		CWD:     "/workspace",
		Command: []string{"go", "test", "./..."},
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if id != "terminal_5" {
		t.Fatalf("CreatePane() id = %q, want terminal_5", id)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{
			"--session", "agent-session",
			"action", "new-pane",
			"--name", "tests",
			"--cwd", "/workspace",
			"--", "go", "test", "./...",
		},
	}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("command = %#v, want %#v", runner.commands[0], want)
	}
}

func TestCreatePaneTargetsTabID(t *testing.T) {
	tabID := TabID(7)
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "terminal_5\n"}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	_, err := backend.CreatePane(context.Background(), CreatePaneRequest{
		Name:    "tests",
		TabID:   &tabID,
		Command: []string{"go", "test", "./..."},
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{
			"action", "new-pane",
			"--name", "tests",
			"--tab-id", "7",
			"--", "go", "test", "./...",
		},
	}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("command = %#v, want %#v", runner.commands[0], want)
	}
}

func TestCreatePaneCanTargetTabZero(t *testing.T) {
	tabID := TabID(0)
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "terminal_5\n"}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	_, err := backend.CreatePane(context.Background(), CreatePaneRequest{
		TabID:   &tabID,
		Command: []string{"pwd"},
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{"action", "new-pane", "--tab-id", "0", "--", "pwd"},
	}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("command = %#v, want %#v", runner.commands[0], want)
	}
}

func TestCreatePaneRejectsEmptyReturnedPaneID(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "\n"}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	_, err := backend.CreatePane(context.Background(), CreatePaneRequest{})
	if !errors.Is(err, ErrEmptyPaneID) {
		t.Fatalf("CreatePane() error = %v, want %v", err, ErrEmptyPaneID)
	}
}

func TestCreateTabParsesReturnedTabID(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "9\n"}},
		},
	}
	backend := NewBackend(Options{
		Session: "agent-session",
		Runner:  runner,
	})

	id, err := backend.CreateTab(context.Background(), CreateTabRequest{
		Name:    "tests",
		CWD:     "/workspace",
		Command: []string{"go", "test", "./..."},
	})
	if err != nil {
		t.Fatalf("CreateTab() error = %v", err)
	}
	if id != 9 {
		t.Fatalf("CreateTab() id = %d, want 9", id)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{
			"--session", "agent-session",
			"action", "new-tab",
			"--name", "tests",
			"--cwd", "/workspace",
			"--", "go", "test", "./...",
		},
	}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("command = %#v, want %#v", runner.commands[0], want)
	}
}

func TestCreateTabRejectsEmptyReturnedTabID(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "\n"}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	_, err := backend.CreateTab(context.Background(), CreateTabRequest{})
	if !errors.Is(err, ErrEmptyTabID) {
		t.Fatalf("CreateTab() error = %v, want %v", err, ErrEmptyTabID)
	}
}

func TestCreateTabAllowsReturnedTabZero(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "0\n"}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	id, err := backend.CreateTab(context.Background(), CreateTabRequest{})
	if err != nil {
		t.Fatalf("CreateTab() error = %v", err)
	}
	if id != 0 {
		t.Fatalf("CreateTab() id = %d, want 0", id)
	}
}

func TestCloseTabCanCloseTabZero(t *testing.T) {
	tabID := TabID(0)
	runner := &fakeRunner{}
	backend := NewBackend(Options{Runner: runner})

	if err := backend.CloseTab(context.Background(), CloseTabRequest{TabID: &tabID}); err != nil {
		t.Fatalf("CloseTab() error = %v", err)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{"action", "close-tab-by-id", "0"},
	}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("command = %#v, want %#v", runner.commands[0], want)
	}
}

func TestListPanesParsesJSONMetadata(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: `[
				{
					"id": 5,
					"is_plugin": false,
					"is_focused": true,
					"is_floating": false,
					"title": "tests",
					"pane_command": "go test ./...",
					"pane_cwd": "/workspace",
					"exited": true,
					"exit_status": 0,
					"tab_id": 1,
					"tab_name": "main",
					"pane_rows": 24,
					"pane_columns": 80,
					"pane_x": 0,
					"pane_y": 1
				}
			]`}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	panes, err := backend.ListPanes(context.Background())
	if err != nil {
		t.Fatalf("ListPanes() error = %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("ListPanes() returned %d panes, want 1", len(panes))
	}

	pane := panes[0]
	if pane.ID != "terminal_5" {
		t.Fatalf("pane.ID = %q, want terminal_5", pane.ID)
	}
	if !pane.IsFocused || pane.Title != "tests" || pane.Command != "go test ./..." || pane.CWD != "/workspace" {
		t.Fatalf("pane metadata parsed incorrectly: %#v", pane)
	}
	if pane.ExitStatus == nil || *pane.ExitStatus != 0 {
		t.Fatalf("pane.ExitStatus = %#v, want 0", pane.ExitStatus)
	}
}

func TestListPanesSurfacesMalformedJSON(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: `not json`}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	_, err := backend.ListPanes(context.Background())
	if err == nil {
		t.Fatal("ListPanes() error = nil, want malformed JSON error")
	}
}

func TestCommandFailureReturnsActionableError(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{
				result: CommandResult{Stderr: "zellij: command not found"},
				err:    errors.New("exit status 127"),
			},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	err := backend.ClosePane(context.Background(), ClosePaneRequest{PaneID: "terminal_5"})
	if err == nil {
		t.Fatal("ClosePane() error = nil, want command error")
	}

	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("ClosePane() error = %T, want *CommandError", err)
	}
	if !strings.Contains(err.Error(), "close pane") || !strings.Contains(err.Error(), "command not found") {
		t.Fatalf("ClosePane() error = %q, want operation and stderr", err.Error())
	}
}

func TestSendInputPreservesPasteThenEnterOrdering(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{}},
			{result: CommandResult{}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	err := backend.SendInput(context.Background(), SendInputRequest{
		PaneID: "terminal_5",
		Text:   "go test ./...\n",
	})
	if err != nil {
		t.Fatalf("SendInput() error = %v", err)
	}

	want := []CommandSpec{
		{
			Name: "zellij",
			Args: []string{"action", "paste", "--pane-id", "terminal_5", "go test ./..."},
		},
		{
			Name: "zellij",
			Args: []string{"action", "send-keys", "--pane-id", "terminal_5", "Enter"},
		},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestDumpScreenReturnsSnapshot(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "PASS\n"}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	output, err := backend.DumpScreen(context.Background(), DumpScreenRequest{
		PaneID: "terminal_5",
		Full:   true,
	})
	if err != nil {
		t.Fatalf("DumpScreen() error = %v", err)
	}
	if output != "PASS\n" {
		t.Fatalf("DumpScreen() output = %q, want PASS", output)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{"action", "dump-screen", "--pane-id", "terminal_5", "--full"},
	}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("command = %#v, want %#v", runner.commands[0], want)
	}
}

func TestSubscribeCommandBuildsJSONStreamCommand(t *testing.T) {
	backend := NewBackend(Options{
		Binary:  "/usr/local/bin/zellij",
		Session: "agent-session",
	})

	spec, err := backend.SubscribeCommand(SubscribeRequest{
		PaneID: "terminal_5",
		JSON:   true,
		ANSI:   true,
	})
	if err != nil {
		t.Fatalf("SubscribeCommand() error = %v", err)
	}

	want := CommandSpec{
		Name: "/usr/local/bin/zellij",
		Args: []string{
			"--session", "agent-session",
			"subscribe",
			"--pane-id", "terminal_5",
			"--format", "json",
			"--ansi",
		},
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("SubscribeCommand() = %#v, want %#v", spec, want)
	}
}

func TestPaneIDIsRequiredForPaneSpecificCommands(t *testing.T) {
	backend := NewBackend(Options{Runner: &fakeRunner{}})

	if err := backend.ClosePane(context.Background(), ClosePaneRequest{}); !errors.Is(err, ErrMissingPane) {
		t.Fatalf("ClosePane() error = %v, want %v", err, ErrMissingPane)
	}
	if err := backend.CloseTab(context.Background(), CloseTabRequest{}); !errors.Is(err, ErrMissingTab) {
		t.Fatalf("CloseTab() error = %v, want %v", err, ErrMissingTab)
	}
	if err := backend.SendInput(context.Background(), SendInputRequest{}); !errors.Is(err, ErrMissingPane) {
		t.Fatalf("SendInput() error = %v, want %v", err, ErrMissingPane)
	}
	if _, err := backend.DumpScreen(context.Background(), DumpScreenRequest{}); !errors.Is(err, ErrMissingPane) {
		t.Fatalf("DumpScreen() error = %v, want %v", err, ErrMissingPane)
	}
	if _, err := backend.SubscribeCommand(SubscribeRequest{}); !errors.Is(err, ErrMissingPane) {
		t.Fatalf("SubscribeCommand() error = %v, want %v", err, ErrMissingPane)
	}
}

type fakeRunner struct {
	commands []CommandSpec
	results  []fakeResult
}

type fakeResult struct {
	result CommandResult
	err    error
}

func (r *fakeRunner) Run(_ context.Context, spec CommandSpec) (CommandResult, error) {
	r.commands = append(r.commands, spec)
	if len(r.results) == 0 {
		return CommandResult{}, nil
	}

	result := r.results[0]
	r.results = r.results[1:]
	return result.result, result.err
}
