package zellij

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
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

func (b *CLIBackend) EnsureSession(ctx context.Context, session string) error {
	session = strings.TrimSpace(session)
	if session == "" {
		return errors.New("session name is required")
	}
	exists := func() (bool, error) {
		sessions, err := b.ActiveSessions(ctx)
		for _, active := range sessions {
			if active == session {
				return true, nil
			}
		}
		return false, err
	}
	if found, err := exists(); err != nil || found {
		return err
	}
	_, err := b.run(ctx, "ensure session", newCommand(b.binary, "", "attach", "--create-background", session))
	if err != nil {
		// Another launch may have created the session after our first check.
		if found, _ := exists(); found {
			return nil
		}
	}
	return err
}

func (b *CLIBackend) requestSession(session string) string {
	if session = strings.TrimSpace(session); session != "" {
		return session
	}
	return b.session
}

func (b *CLIBackend) CreateTab(ctx context.Context, req CreateTabRequest) (TabID, error) {
	session := b.requestSession(req.Session)
	stop, temporary, err := b.prepareTabClient(ctx, session)
	if err != nil {
		return 0, err
	}
	defer stop()
	result, err := b.run(ctx, "create tab", createTabCommand(b.binary, b.requestSession(req.Session), req))
	if err != nil {
		return 0, err
	}

	id, err := parseTabID(result.Stdout)
	if err != nil {
		return 0, err
	}
	if temporary {
		// The command returns the tab ID before its layout is applied.
		waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			panes, listErr := b.ListPanes(waitCtx, ListPanesRequest{Session: session})
			if listErr != nil {
				break
			}
			for _, pane := range panes {
				if TabID(pane.TabID) == id && !pane.IsPlugin {
					return id, nil
				}
			}
			select {
			case <-waitCtx.Done():
				return id, nil
			case <-ticker.C:
			}
		}
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

	if req.TargetSession == "" {
		return fmt.Errorf("target: %w", ErrMissingSession)
	}
	if req.TargetPaneID == "" {
		return fmt.Errorf("target: %w", ErrMissingPane)
	}
	if req.SourceSession == "" {
		session, err := b.connectedSession(ctx)
		if err != nil {
			return err
		}
		req.SourceSession = session
	}
	if req.SourceSession == req.TargetSession && req.SourcePaneID == req.TargetPaneID {
		return nil
	}

	_, err := b.run(ctx, "switch session", switchSessionCommand(b.binary, req))
	if isAlreadyFocusedError(err) {
		return nil
	}
	return err
}

func isAlreadyFocusedError(err error) bool {
	var commandErr *CommandError
	return errors.As(err, &commandErr) && strings.Contains(commandErr.Stderr, "is already focused")
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

// connectedSession resolves navigation at the runtime boundary without requiring
// the caller to inherit a terminal's environment. Never choose between clients.
func (b *CLIBackend) connectedSession(ctx context.Context) (string, error) {
	result, err := b.run(ctx, "list sessions", newCommand(b.binary, "", "list-sessions", "--short", "--no-formatting"))
	if err != nil {
		return "", err
	}
	var connected string
	for _, session := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		session = strings.TrimSpace(session)
		if session == "" || strings.Contains(session, "EXITED") {
			continue
		}
		clients, err := b.run(ctx, "list clients", newActionCommand(b.binary, session, "list-clients"))
		if err != nil {
			return "", err
		}
		rows := strings.Split(strings.TrimSpace(clients.Stdout), "\n")
		for _, row := range rows[1:] {
			if strings.TrimSpace(row) == "" {
				continue
			}
			if connected != "" {
				return "", fmt.Errorf("agent navigation requires exactly one connected Zellij client")
			}
			connected = session
		}
	}
	if connected == "" {
		return "", fmt.Errorf("agent navigation requires a connected Zellij client")
	}
	return connected, nil
}

// ActiveSessions includes detached sessions and excludes exited sessions.
func (b *CLIBackend) ActiveSessions(ctx context.Context) ([]string, error) {
	// --short removes the EXITED marker as well as the age, so it cannot
	// distinguish a live session from one that can only be resurrected.
	spec := newCommand(b.binary, "", "list-sessions", "--no-formatting")
	result, err := b.runner.Run(ctx, spec)
	if err != nil {
		if strings.TrimSpace(result.Stderr) == "No active zellij sessions found." || strings.TrimSpace(result.Stdout) == "No active zellij sessions found." {
			return nil, nil
		}
		return nil, &CommandError{Operation: "list sessions", Spec: spec, Stderr: result.Stderr, Err: err}
	}
	var sessions []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.Contains(line, "EXITED") {
			name, _, _ := strings.Cut(line, " [Created ")
			sessions = append(sessions, name)
		}
	}
	return sessions, nil
}
