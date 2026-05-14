package zellij

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
)

type CLIBackend struct {
	binary  string
	session string
	runner  CommandRunner

	locksMu   sync.Mutex
	paneLocks map[PaneID]*sync.Mutex
}

func NewBackend(opts Options) *CLIBackend {
	binary := opts.Binary
	if binary == "" {
		binary = defaultBinary
	}

	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}

	return &CLIBackend{
		binary:    binary,
		session:   opts.Session,
		runner:    runner,
		paneLocks: make(map[PaneID]*sync.Mutex),
	}
}

func (b *CLIBackend) CreatePane(ctx context.Context, req CreatePaneRequest) (PaneID, error) {
	result, err := b.run(ctx, "create pane", createPaneCommand(b.binary, b.session, req))
	if err != nil {
		return "", err
	}

	id, err := cleanPaneID(result.Stdout)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (b *CLIBackend) ClosePane(ctx context.Context, req ClosePaneRequest) error {
	if req.PaneID == "" {
		return ErrMissingPane
	}

	_, err := b.run(ctx, "close pane", closePaneCommand(b.binary, b.session, req.PaneID))
	return err
}

func (b *CLIBackend) SendInput(ctx context.Context, req SendInputRequest) error {
	if req.PaneID == "" {
		return ErrMissingPane
	}

	lock := b.lockForPane(req.PaneID)
	lock.Lock()
	defer lock.Unlock()

	text := req.Text
	sendEnter := strings.HasSuffix(text, "\n")
	if sendEnter {
		text = strings.TrimSuffix(text, "\n")
	}

	if text != "" {
		if _, err := b.run(ctx, "paste input", pasteCommand(b.binary, b.session, req.PaneID, text)); err != nil {
			return err
		}
	}

	if sendEnter {
		if _, err := b.run(ctx, "send enter", sendEnterCommand(b.binary, b.session, req.PaneID)); err != nil {
			return err
		}
	}

	return nil
}

func (b *CLIBackend) ListPanes(ctx context.Context) ([]Pane, error) {
	result, err := b.run(ctx, "list panes", listPanesCommand(b.binary, b.session))
	if err != nil {
		return nil, err
	}

	var panes []Pane
	if err := json.Unmarshal([]byte(result.Stdout), &panes); err != nil {
		return nil, err
	}
	return panes, nil
}

func (b *CLIBackend) DumpScreen(ctx context.Context, req DumpScreenRequest) (string, error) {
	if req.PaneID == "" {
		return "", ErrMissingPane
	}

	result, err := b.run(ctx, "dump screen", dumpScreenCommand(b.binary, b.session, req))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (b *CLIBackend) SubscribeCommand(req SubscribeRequest) (CommandSpec, error) {
	if req.PaneID == "" {
		return CommandSpec{}, ErrMissingPane
	}
	return subscribeCommand(b.binary, b.session, req), nil
}

func (b *CLIBackend) run(ctx context.Context, operation string, spec CommandSpec) (CommandResult, error) {
	result, err := b.runner.Run(ctx, spec)
	if err != nil {
		return CommandResult{}, &CommandError{
			Operation: operation,
			Spec:      spec,
			Stderr:    result.Stderr,
			Err:       err,
		}
	}
	return result, nil
}

func (b *CLIBackend) lockForPane(id PaneID) *sync.Mutex {
	b.locksMu.Lock()
	defer b.locksMu.Unlock()

	lock, ok := b.paneLocks[id]
	if !ok {
		lock = &sync.Mutex{}
		b.paneLocks[id] = lock
	}
	return lock
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err != nil {
		return result, err
	}

	return result, nil
}
