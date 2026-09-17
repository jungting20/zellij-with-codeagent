package zellij

import (
	"context"
	"errors"
	"testing"
)

type sessionClientFakeRunner struct {
	*fakeRunner
	started, stopped int
	startErr         error
}

func (r *sessionClientFakeRunner) startSessionClient(context.Context, string, string) (func(), error) {
	r.started++
	return func() { r.stopped++ }, r.startErr
}

func TestCreateTabKeepsTemporaryClientUntilTerminalAppears(t *testing.T) {
	runner := &sessionClientFakeRunner{fakeRunner: &fakeRunner{results: []fakeResult{
		{result: CommandResult{Stdout: "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n"}},
		{result: CommandResult{Stdout: "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n1 terminal_0 zsh\n"}},
		{result: CommandResult{Stdout: "7\n"}},
		{result: CommandResult{Stdout: `[{"id":"plugin_1","is_plugin":true,"tab_id":7}]`}},
		{result: CommandResult{Stdout: `[{"id":"terminal_2","tab_id":7}]`}},
	}}}
	id, err := NewBackend(Options{Runner: runner}).CreateTab(context.Background(), CreateTabRequest{Session: "detached", Name: "parent"})
	if err != nil || id != 7 || runner.started != 1 || runner.stopped != 1 || len(runner.commands) != 5 {
		t.Fatalf("id=%d error=%v started=%d stopped=%d commands=%+v", id, err, runner.started, runner.stopped, runner.commands)
	}
}

func TestCreateTabDoesNotAttachToConnectedSession(t *testing.T) {
	runner := &sessionClientFakeRunner{fakeRunner: &fakeRunner{results: []fakeResult{
		{result: CommandResult{Stdout: "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n1 terminal_0 zsh\n"}},
		{result: CommandResult{Stdout: "7\n"}},
	}}}
	_, err := NewBackend(Options{Runner: runner}).CreateTab(context.Background(), CreateTabRequest{Session: "connected"})
	if err != nil || runner.started != 0 {
		t.Fatalf("error=%v started=%d", err, runner.started)
	}
}

func TestCreateTabFailureReleasesTemporaryClient(t *testing.T) {
	runner := &sessionClientFakeRunner{fakeRunner: &fakeRunner{results: []fakeResult{
		{result: CommandResult{Stdout: "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n"}},
		{result: CommandResult{Stdout: "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n1 terminal_0 zsh\n"}},
		{err: errors.New("tab failed")},
	}}}
	_, err := NewBackend(Options{Runner: runner}).CreateTab(context.Background(), CreateTabRequest{Session: "detached"})
	if err == nil || runner.stopped != 1 {
		t.Fatalf("error=%v stopped=%d", err, runner.stopped)
	}
}
