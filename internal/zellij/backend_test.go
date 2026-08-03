package zellij

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBackendRequestsUseRequestSession(t *testing.T) {
	wantPrefix := []string{"--session", "request-session"}
	tabID := TabID(7)

	tests := []struct {
		name   string
		result CommandResult
		invoke func(*CLIBackend) error
	}{
		{
			name:   "create tab",
			result: CommandResult{Stdout: "7\n"},
			invoke: func(backend *CLIBackend) error {
				_, err := backend.CreateTab(context.Background(), CreateTabRequest{Session: "request-session"})
				return err
			},
		},
		{
			name:   "create pane",
			result: CommandResult{Stdout: "terminal_5\n"},
			invoke: func(backend *CLIBackend) error {
				_, err := backend.CreatePane(context.Background(), CreatePaneRequest{Session: "request-session"})
				return err
			},
		},
		{
			name:   "list panes",
			result: CommandResult{Stdout: "[]"},
			invoke: func(backend *CLIBackend) error {
				_, err := backend.ListPanes(context.Background(), ListPanesRequest{Session: "request-session"})
				return err
			},
		},
		{
			name: "close tab",
			invoke: func(backend *CLIBackend) error {
				return backend.CloseTab(context.Background(), CloseTabRequest{Session: "request-session", TabID: &tabID})
			},
		},
		{
			name: "close pane",
			invoke: func(backend *CLIBackend) error {
				return backend.ClosePane(context.Background(), ClosePaneRequest{Session: "request-session", PaneID: "terminal_5"})
			},
		},
		{
			name: "send input",
			invoke: func(backend *CLIBackend) error {
				return backend.SendInput(context.Background(), SendInputRequest{Session: "request-session", PaneID: "terminal_5", Text: "hello"})
			},
		},
		{
			name: "dump screen",
			invoke: func(backend *CLIBackend) error {
				_, err := backend.DumpScreen(context.Background(), DumpScreenRequest{Session: "request-session", PaneID: "terminal_5"})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{results: []fakeResult{{result: tt.result}}}
			backend := NewBackend(Options{Session: "default-session", Runner: runner})

			if err := tt.invoke(backend); err != nil {
				t.Fatalf("request error = %v", err)
			}
			if !reflect.DeepEqual(runner.commands[0].Args[:2], wantPrefix) {
				t.Fatalf("command prefix = %#v, want %#v", runner.commands[0].Args[:2], wantPrefix)
			}
		})
	}
}

func TestBackendRequestSessionFallsBackToOptionsSession(t *testing.T) {
	runner := &fakeRunner{results: []fakeResult{{result: CommandResult{Stdout: "terminal_5\n"}}}}
	backend := NewBackend(Options{Session: "default-session", Runner: runner})

	if _, err := backend.CreatePane(context.Background(), CreatePaneRequest{}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	wantPrefix := []string{"--session", "default-session"}
	if !reflect.DeepEqual(runner.commands[0].Args[:2], wantPrefix) {
		t.Fatalf("command prefix = %#v, want %#v", runner.commands[0].Args[:2], wantPrefix)
	}
}

func TestSwitchSessionUsesSourceContextAndTargetPane(t *testing.T) {
	runner := &fakeRunner{}
	backend := NewBackend(Options{Runner: runner})

	err := backend.SwitchSession(context.Background(), SwitchSessionRequest{
		SourceSession: "dashboard-session",
		SourcePaneID:  "terminal_2",
		TargetSession: "target-session",
		TargetPaneID:  "terminal_12",
	})
	if err != nil {
		t.Fatalf("SwitchSession() error = %v", err)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{
			"--session", "dashboard-session",
			"action", "switch-session", "target-session",
			"--pane-id", "terminal_12",
		},
		Env: []string{
			"ZELLIJ_SESSION_NAME=dashboard-session",
			"ZELLIJ_PANE_ID=terminal_2",
		},
	}
	if !reflect.DeepEqual(runner.commands, []CommandSpec{want}) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, []CommandSpec{want})
	}
}

func TestSwitchSessionFocusesPaneDirectlyWithinSourceSession(t *testing.T) {
	runner := &fakeRunner{}
	backend := NewBackend(Options{Runner: runner})

	err := backend.SwitchSession(context.Background(), SwitchSessionRequest{
		SourceSession: "shared-session",
		SourcePaneID:  "terminal_2",
		TargetSession: "shared-session",
		TargetPaneID:  "terminal_12",
	})
	if err != nil {
		t.Fatalf("SwitchSession() error = %v", err)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{
			"--session", "shared-session",
			"action", "focus-pane-id", "terminal_12",
		},
		Env: []string{
			"ZELLIJ_SESSION_NAME=shared-session",
			"ZELLIJ_PANE_ID=terminal_2",
		},
	}
	if !reflect.DeepEqual(runner.commands, []CommandSpec{want}) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, []CommandSpec{want})
	}
}

func TestSwitchSessionRejectsMissingContextBeforeRunningCommand(t *testing.T) {
	tests := []struct {
		name string
		req  SwitchSessionRequest
	}{
		{
			name: "source session",
			req:  SwitchSessionRequest{SourcePaneID: "terminal_2", TargetSession: "target-session", TargetPaneID: "terminal_12"},
		},
		{
			name: "source pane",
			req:  SwitchSessionRequest{SourceSession: "dashboard-session", TargetSession: "target-session", TargetPaneID: "terminal_12"},
		},
		{
			name: "target session",
			req:  SwitchSessionRequest{SourceSession: "dashboard-session", SourcePaneID: "terminal_2", TargetPaneID: "terminal_12"},
		},
		{
			name: "target pane",
			req:  SwitchSessionRequest{SourceSession: "dashboard-session", SourcePaneID: "terminal_2", TargetSession: "target-session"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{}
			backend := NewBackend(Options{Runner: runner})

			if err := backend.SwitchSession(context.Background(), tt.req); err == nil {
				t.Fatal("SwitchSession() error = nil")
			}
			if len(runner.commands) != 0 {
				t.Fatalf("commands = %#v, want none", runner.commands)
			}
		})
	}
}

func TestExecRunnerEnvAppendsToProcessEnvironment(t *testing.T) {
	t.Setenv("CODEAGENT_EXEC_RUNNER_BASE", "inherited")

	result, err := (ExecRunner{}).Run(context.Background(), CommandSpec{
		Name: "sh",
		Args: []string{"-c", `printf '%s|%s' "$CODEAGENT_EXEC_RUNNER_BASE" "$CODEAGENT_EXEC_RUNNER_ADDED"`},
		Env:  []string{"CODEAGENT_EXEC_RUNNER_ADDED=appended"},
	})
	if err != nil {
		t.Fatalf("ExecRunner.Run() error = %v", err)
	}
	if result.Stdout != "inherited|appended" {
		t.Fatalf("ExecRunner.Run() stdout = %q, want inherited|appended", result.Stdout)
	}
}

func TestSubscribeRequestUsesRequestSession(t *testing.T) {
	backend := NewBackend(Options{Session: "default-session"})

	spec, err := backend.SubscribeCommand(SubscribeRequest{Session: "request-session", PaneID: "terminal_5"})
	if err != nil {
		t.Fatalf("SubscribeCommand() error = %v", err)
	}

	wantPrefix := []string{"--session", "request-session"}
	if !reflect.DeepEqual(spec.Args[:2], wantPrefix) {
		t.Fatalf("command prefix = %#v, want %#v", spec.Args[:2], wantPrefix)
	}
}

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

func TestCreatePaneCanCreateFloatingPane(t *testing.T) {
	tabID := TabID(7)
	runner := &fakeRunner{
		results: []fakeResult{
			{result: CommandResult{Stdout: "terminal_5\n"}},
		},
	}
	backend := NewBackend(Options{Runner: runner})

	_, err := backend.CreatePane(context.Background(), CreatePaneRequest{
		Name:     "monitor",
		TabID:    &tabID,
		Floating: true,
		Command:  []string{"sh"},
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	want := CommandSpec{
		Name: "zellij",
		Args: []string{
			"action", "new-pane",
			"--name", "monitor",
			"--tab-id", "7",
			"--floating",
			"--", "sh",
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
		Name:         "tests",
		CWD:          "/workspace",
		LayoutString: "layout { pane; }",
		Command:      []string{"go", "test", "./..."},
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
			"--layout-string", "layout { pane; }",
			"--name", "tests",
			"--cwd", "/workspace",
			"--", "go", "test", "./...",
		},
	}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("command = %#v, want %#v", runner.commands[0], want)
	}
}

func TestCreateTabOmitsEmptyLayoutString(t *testing.T) {
	runner := &fakeRunner{results: []fakeResult{{result: CommandResult{Stdout: "4\n"}}}}
	backend := NewBackend(Options{Runner: runner})

	if _, err := backend.CreateTab(context.Background(), CreateTabRequest{Name: "plain"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"action", "new-tab", "--name", "plain"}
	if !reflect.DeepEqual(runner.commands[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, want)
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

	panes, err := backend.ListPanes(context.Background(), ListPanesRequest{})
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

	_, err := backend.ListPanes(context.Background(), ListPanesRequest{})
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
			Args: []string{"action", "paste", "--pane-id", "terminal_5", "--", "go test ./..."},
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

func TestSendInputTreatsLeadingHyphenAsPasteText(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{{result: CommandResult{}}},
	}
	backend := NewBackend(Options{Runner: runner})

	err := backend.SendInput(context.Background(), SendInputRequest{
		PaneID: "terminal_5",
		Text:   "--help",
	})
	if err != nil {
		t.Fatalf("SendInput() error = %v", err)
	}

	want := []CommandSpec{{
		Name: "zellij",
		Args: []string{"action", "paste", "--pane-id", "terminal_5", "--", "--help"},
	}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want leading-hyphen text after -- delimiter %#v", runner.commands, want)
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
