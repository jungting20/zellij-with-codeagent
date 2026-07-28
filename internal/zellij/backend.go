package zellij

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
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

func (b *CLIBackend) Session() string {
	return b.session
}

func (b *CLIBackend) requestSession(session string) string {
	if session = strings.TrimSpace(session); session != "" {
		return session
	}
	return b.session
}

func (b *CLIBackend) CreateTab(ctx context.Context, req CreateTabRequest) (TabID, error) {
	result, err := b.run(ctx, "create tab", createTabCommand(b.binary, b.requestSession(req.Session), req))
	if err != nil {
		return 0, err
	}

	id, err := parseTabID(result.Stdout)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (b *CLIBackend) CloseTab(ctx context.Context, req CloseTabRequest) error {
	if req.TabID == nil {
		return ErrMissingTab
	}

	_, err := b.run(ctx, "close tab", closeTabCommand(b.binary, b.requestSession(req.Session), *req.TabID))
	return err
}

func (b *CLIBackend) CreatePane(ctx context.Context, req CreatePaneRequest) (PaneID, error) {
	result, err := b.run(ctx, "create pane", createPaneCommand(b.binary, b.requestSession(req.Session), req))
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

	_, err := b.run(ctx, "close pane", closePaneCommand(b.binary, b.requestSession(req.Session), req.PaneID))
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
	session := b.requestSession(req.Session)

	if text != "" {
		if _, err := b.run(ctx, "paste input", pasteCommand(b.binary, session, req.PaneID, text)); err != nil {
			return err
		}
	}

	if sendEnter {
		if _, err := b.run(ctx, "send enter", sendEnterCommand(b.binary, session, req.PaneID)); err != nil {
			return err
		}
	}

	return nil
}

func (b *CLIBackend) ListPanes(ctx context.Context, req ListPanesRequest) ([]Pane, error) {
	result, err := b.run(ctx, "list panes", listPanesCommand(b.binary, b.requestSession(req.Session)))
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

	result, err := b.run(ctx, "dump screen", dumpScreenCommand(b.binary, b.requestSession(req.Session), req))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (b *CLIBackend) SubscribeCommand(req SubscribeRequest) (CommandSpec, error) {
	if req.PaneID == "" {
		return CommandSpec{}, ErrMissingPane
	}
	return subscribeCommand(b.binary, b.requestSession(req.Session), req), nil
}

func (b *CLIBackend) SwitchSession(ctx context.Context, req SwitchSessionRequest) error {
	req.SourceSession = strings.TrimSpace(req.SourceSession)
	req.SourcePaneID = PaneID(strings.TrimSpace(string(req.SourcePaneID)))
	req.TargetSession = strings.TrimSpace(req.TargetSession)
	req.TargetPaneID = PaneID(strings.TrimSpace(string(req.TargetPaneID)))

	if req.SourceSession == "" {
		return fmt.Errorf("source: %w", ErrMissingSession)
	}
	if req.SourcePaneID == "" {
		return fmt.Errorf("source: %w", ErrMissingPane)
	}
	if req.TargetSession == "" {
		return fmt.Errorf("target: %w", ErrMissingSession)
	}
	if req.TargetPaneID == "" {
		return fmt.Errorf("target: %w", ErrMissingPane)
	}

	_, err := b.run(ctx, "switch session", switchSessionCommand(b.binary, req))
	return err
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
	cmd.Env = append(os.Environ(), spec.Env...)

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
