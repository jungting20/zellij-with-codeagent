package ticketworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"zellij-with-codeagent/internal/transport"
)

type ManagerClient interface {
	CreatePane(context.Context, transport.CreatePaneRequest) (transport.CreatePaneResponse, error)
	WaitForOutputMarker(context.Context, string, transport.WaitForOutputMarkerRequest) (transport.WaitForOutputMarkerResponse, error)
	ClosePane(context.Context, string) (transport.ClosePaneResponse, error)
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
}

type ManagerOptions struct {
	Client           ManagerClient
	Config           Config
	TaskID           string
	AnchorPaneID     string
	CWD              string
	ZellijSession    string
	StartupTimeout   time.Duration
	Tick             <-chan time.Time
	Now              func() time.Time
	Log              io.Writer
	CompletionRunner CompletionRunner
}

type Manager struct {
	client            ManagerClient
	config            Config
	taskID            string
	anchorPaneID      string
	cwd               string
	zellijSession     string
	startupTimeout    time.Duration
	tick              <-chan time.Time
	now               func() time.Time
	log               io.Writer
	slots             []workerSlot
	watchResults      chan watchResult
	completionRunner  CompletionRunner
	completionResults chan completionRunResult
	beforeClose       func()
	afterEvent        func()
}

type slotState uint8

const (
	slotEmpty slotState = iota
	slotOccupied
	slotCompleting
	slotCompletionFailed
)

type workerSlot struct {
	number      int
	sequence    uint64
	paneID      string
	state       slotState
	startedAt   time.Time
	completedAt time.Time
	lastError   string
	ticketID    string
}

type watchResult struct {
	slotNumber int
	paneID     string
	response   transport.WaitForOutputMarkerResponse
	err        error
}

type completionRunResult struct {
	slotNumber int
	paneID     string
	ticketID   string
	matchedAt  time.Time
	result     CompletionResult
}

func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.Client == nil {
		return nil, fmt.Errorf("ticket-worker manager client is required")
	}
	if err := validateManagerConfig(opts.Config); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.TaskID) == "" {
		return nil, fmt.Errorf("ticket-worker manager task ID is required")
	}
	if strings.TrimSpace(opts.AnchorPaneID) == "" {
		return nil, fmt.Errorf("ticket-worker manager anchor pane ID is required")
	}
	zellijSession := strings.TrimSpace(opts.ZellijSession)
	if zellijSession == "" {
		return nil, fmt.Errorf("ticket-worker manager zellij session is required")
	}
	startupTimeout := opts.StartupTimeout
	if startupTimeout < 0 {
		return nil, fmt.Errorf("ticket-worker manager startup timeout must not be negative")
	}
	if startupTimeout == 0 {
		startupTimeout = 15 * time.Second
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	completionRunner := opts.CompletionRunner
	if completionRunner == nil {
		completionRunner = ExecCompletionRunner{}
	}
	slots := make([]workerSlot, opts.Config.MaxWorkers)
	for i := range slots {
		slots[i].number = i + 1
	}

	return &Manager{
		client:            opts.Client,
		config:            opts.Config,
		taskID:            opts.TaskID,
		anchorPaneID:      opts.AnchorPaneID,
		cwd:               opts.CWD,
		zellijSession:     zellijSession,
		startupTimeout:    startupTimeout,
		tick:              opts.Tick,
		now:               now,
		log:               log,
		slots:             slots,
		watchResults:      make(chan watchResult, opts.Config.MaxWorkers),
		completionRunner:  completionRunner,
		completionResults: make(chan completionRunResult, opts.Config.MaxWorkers),
	}, nil
}

func (m *Manager) Run(ctx context.Context) error {
	if err := m.waitForAnchor(ctx); err != nil {
		return err
	}

	ticks := m.tick
	var ticker *time.Ticker
	if ticks == nil {
		ticker = time.NewTicker(m.config.PollInterval)
		defer ticker.Stop()
		ticks = ticker.C
	}

	m.fillEmptySlots(ctx)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ticks:
			if !ok {
				ticks = nil
				continue
			}
			m.reconcileFailedSlots(ctx)
			m.fillEmptySlots(ctx)
			m.notifyEventProcessed()
		case result := <-m.watchResults:
			m.handleWatchResult(ctx, result)
			m.notifyEventProcessed()
		case result := <-m.completionResults:
			m.handleCompletionResult(ctx, result)
			m.notifyEventProcessed()
		}
	}
}

func (m *Manager) waitForAnchor(ctx context.Context) error {
	readyCtx, cancel := context.WithTimeout(ctx, m.startupTimeout)
	defer cancel()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()

	var lastInspectionErr error
	for {
		response, err := m.client.InspectRuntime(readyCtx)
		if readyErr := readyCtx.Err(); readyErr != nil {
			if err != nil {
				lastInspectionErr = err
			}
			return m.anchorNotReadyError(readyErr, lastInspectionErr)
		}
		if err != nil {
			lastInspectionErr = err
		} else if m.runtimeHasPane(response, m.anchorPaneID, "starting", "running") {
			return nil
		}

		select {
		case <-readyCtx.Done():
			return m.anchorNotReadyError(readyCtx.Err(), lastInspectionErr)
		case <-poll.C:
		}
	}
}

func (m *Manager) anchorNotReadyError(readinessErr, inspectionErr error) error {
	anchorErr := fmt.Errorf(
		"ticket-worker manager anchor not ready: pane %q task %q zellij session %q: %w",
		m.anchorPaneID, m.taskID, m.zellijSession, readinessErr,
	)
	if inspectionErr != nil {
		return errors.Join(anchorErr, inspectionErr)
	}
	return anchorErr
}

func (m *Manager) runtimeHasPane(response transport.InspectRuntimeResponse, paneID string, statuses ...string) bool {
	for _, pane := range response.Panes {
		if pane.ID != paneID || pane.TaskID != m.taskID || pane.SessionID != m.zellijSession {
			continue
		}
		if len(statuses) == 0 {
			return true
		}
		for _, status := range statuses {
			if pane.Status == status {
				return true
			}
		}
	}
	return false
}

func (m *Manager) fillEmptySlots(ctx context.Context) {
	for i := range m.slots {
		if ctx.Err() != nil {
			return
		}
		if m.slots[i].state == slotEmpty {
			m.launchSlot(ctx, &m.slots[i])
		}
	}
}

func (m *Manager) launchSlot(ctx context.Context, slot *workerSlot) {
	slot.sequence++
	paneID := fmt.Sprintf("ticket-worker-slot-%d-%04d", slot.number, slot.sequence)
	req := transport.CreatePaneRequest{
		ID:              paneID,
		TaskID:          m.taskID,
		Role:            "ticket-worker",
		Name:            paneID,
		SameTabAsPaneID: m.anchorPaneID,
		Command:         append([]string(nil), m.config.Worker.Command...),
		CWD:             m.cwd,
		ZellijSession:   m.zellijSession,
	}
	m.logf("create slot=%d pane=%s", slot.number, paneID)
	if _, err := m.client.CreatePane(ctx, req); err != nil {
		slot.lastError = err.Error()
		m.logf("create failed slot=%d pane=%s error=%v", slot.number, paneID, err)
		return
	}

	slot.paneID = paneID
	slot.state = slotOccupied
	slot.startedAt = m.now()
	slot.completedAt = time.Time{}
	slot.lastError = ""
	slot.ticketID = ""
	marker := m.config.Worker.CompletionMarker
	matchPrefix := len(m.config.Worker.CompleteCommand) > 0
	if matchPrefix {
		marker += " "
	}
	m.logf("watch slot=%d pane=%s marker=%q", slot.number, paneID, marker)
	go m.watch(ctx, slot.number, paneID)
}

func (m *Manager) watch(ctx context.Context, slotNumber int, paneID string) {
	marker := m.config.Worker.CompletionMarker
	matchPrefix := len(m.config.Worker.CompleteCommand) > 0
	if matchPrefix {
		marker += " "
	}
	response, err := m.client.WaitForOutputMarker(ctx, paneID, transport.WaitForOutputMarkerRequest{
		Marker: marker, MatchPrefix: matchPrefix,
	})
	result := watchResult{slotNumber: slotNumber, paneID: paneID, response: response, err: err}
	select {
	case m.watchResults <- result:
	case <-ctx.Done():
	}
}

func (m *Manager) handleWatchResult(ctx context.Context, result watchResult) {
	if ctx.Err() != nil {
		return
	}
	if result.slotNumber < 1 || result.slotNumber > len(m.slots) {
		return
	}
	slot := &m.slots[result.slotNumber-1]
	if slot.state != slotOccupied || slot.paneID != result.paneID {
		return
	}
	if result.err != nil {
		slot.lastError = result.err.Error()
		m.logf("watch failed slot=%d pane=%s error=%v", slot.number, slot.paneID, result.err)
		return
	}
	expectedMarker := m.config.Worker.CompletionMarker
	structured := len(m.config.Worker.CompleteCommand) > 0
	if structured {
		expectedMarker += " "
	}
	if result.response.PaneID != result.paneID || result.response.Marker != expectedMarker {
		slot.lastError = fmt.Sprintf("unexpected watch response pane=%q marker=%q", result.response.PaneID, result.response.Marker)
		m.logf("watch rejected slot=%d pane=%s response_pane=%q marker=%q", slot.number, slot.paneID, result.response.PaneID, result.response.Marker)
		return
	}
	if structured {
		ticketID, err := parseCompletionLine(m.config.Worker.CompletionMarker, result.response.MatchedLine)
		if err != nil {
			slot.state = slotCompletionFailed
			slot.lastError = err.Error()
			m.logf("completion rejected slot=%d pane=%s error=%v", slot.number, slot.paneID, err)
			return
		}
		slot.state = slotCompleting
		slot.ticketID = ticketID
		m.logf("complete ticket slot=%d pane=%s ticket=%s", slot.number, slot.paneID, ticketID)
		go m.runCompletion(ctx, slot.number, slot.paneID, ticketID, result.response.MatchedAt)
		return
	}

	m.closeCompletedSlot(ctx, slot, result.response.MatchedAt)
}

func (m *Manager) runCompletion(ctx context.Context, slotNumber int, paneID, ticketID string, matchedAt time.Time) {
	timeout := m.config.Worker.CompleteTimeout
	if timeout <= 0 {
		timeout = defaultCompleteTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := m.completionRunner.Run(runCtx, CompletionRequest{
		Command: append([]string(nil), m.config.Worker.CompleteCommand...), TicketID: ticketID, CWD: m.cwd,
	})
	completed := completionRunResult{slotNumber: slotNumber, paneID: paneID, ticketID: ticketID, matchedAt: matchedAt, result: result}
	select {
	case m.completionResults <- completed:
	case <-ctx.Done():
	}
}

func (m *Manager) handleCompletionResult(ctx context.Context, completed completionRunResult) {
	if ctx.Err() != nil || completed.slotNumber < 1 || completed.slotNumber > len(m.slots) {
		return
	}
	slot := &m.slots[completed.slotNumber-1]
	if slot.state != slotCompleting || slot.paneID != completed.paneID || slot.ticketID != completed.ticketID {
		return
	}
	if completed.result.Err != nil {
		slot.state = slotCompletionFailed
		slot.lastError = completed.result.Err.Error()
		if completed.result.Output != "" {
			slot.lastError += ": " + completed.result.Output
		}
		m.logf("complete ticket failed slot=%d pane=%s ticket=%s error=%s", slot.number, slot.paneID, slot.ticketID, slot.lastError)
		return
	}
	m.logf("complete ticket succeeded slot=%d pane=%s ticket=%s", slot.number, slot.paneID, slot.ticketID)
	m.closeCompletedSlot(ctx, slot, completed.matchedAt)
}

func (m *Manager) closeCompletedSlot(ctx context.Context, slot *workerSlot, matchedAt time.Time) {
	m.logf("close slot=%d pane=%s", slot.number, slot.paneID)
	if m.beforeClose != nil {
		m.beforeClose()
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := m.client.ClosePane(ctx, slot.paneID); err != nil {
		slot.state = slotOccupied
		slot.lastError = err.Error()
		m.logf("close failed slot=%d pane=%s error=%v", slot.number, slot.paneID, err)
		response, inspectErr := m.client.InspectRuntime(ctx)
		if inspectErr != nil {
			m.logf("close reconciliation failed slot=%d pane=%s error=%v", slot.number, slot.paneID, inspectErr)
			return
		}
		if m.runtimeHasPane(response, slot.paneID) {
			return
		}
		m.logf("already closed slot=%d pane=%s", slot.number, slot.paneID)
		m.completeSlot(slot, matchedAt)
		return
	}

	m.logf("closed slot=%d pane=%s", slot.number, slot.paneID)
	m.completeSlot(slot, matchedAt)
}

func (m *Manager) reconcileFailedSlots(ctx context.Context) {
	hasFailed := false
	for i := range m.slots {
		if m.slots[i].state == slotCompletionFailed {
			hasFailed = true
			break
		}
	}
	if !hasFailed || ctx.Err() != nil {
		return
	}
	response, err := m.client.InspectRuntime(ctx)
	if err != nil {
		m.logf("completion reconciliation failed error=%v", err)
		return
	}
	for i := range m.slots {
		slot := &m.slots[i]
		if slot.state != slotCompletionFailed || m.runtimeHasPane(response, slot.paneID, "starting", "running") {
			continue
		}
		m.logf("completion manually reconciled slot=%d pane=%s ticket=%s", slot.number, slot.paneID, slot.ticketID)
		m.completeSlot(slot, time.Time{})
	}
}

func (m *Manager) completeSlot(slot *workerSlot, matchedAt time.Time) {
	slot.completedAt = matchedAt
	if slot.completedAt.IsZero() {
		slot.completedAt = m.now()
	}
	slot.paneID = ""
	slot.state = slotEmpty
	slot.lastError = ""
	slot.ticketID = ""
}

func (m *Manager) logf(format string, args ...any) {
	fmt.Fprintf(m.log, "ticket-worker: "+format+"\n", args...)
}

func (m *Manager) notifyEventProcessed() {
	if m.afterEvent != nil {
		m.afterEvent()
	}
}

func validateManagerConfig(cfg Config) error {
	if cfg.MaxWorkers <= 0 {
		return fmt.Errorf("max_workers must be positive")
	}
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("poll_interval must be positive")
	}
	if len(cfg.Worker.Command) == 0 {
		return fmt.Errorf("worker.command must not be empty")
	}
	for i, arg := range cfg.Worker.Command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("worker.command[%d] must not be empty", i)
		}
	}
	marker := cfg.Worker.CompletionMarker
	if marker != strings.TrimSpace(marker) {
		return fmt.Errorf("worker.completion_marker must not have surrounding whitespace")
	}
	if strings.ContainsAny(marker, "\r\n") {
		return fmt.Errorf("worker.completion_marker must be a single line")
	}
	if marker == "" {
		return fmt.Errorf("worker.completion_marker must not be empty")
	}
	for i, arg := range cfg.Worker.CompleteCommand {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("worker.complete_command[%d] must not be empty", i)
		}
	}
	if len(cfg.Worker.CompleteCommand) > 0 && cfg.Worker.CompleteTimeout < 0 {
		return fmt.Errorf("worker.complete_timeout must not be negative")
	}
	return nil
}
