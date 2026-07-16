package ticketworker

import (
	"context"
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
}

type ManagerOptions struct {
	Client       ManagerClient
	Config       Config
	TaskID       string
	AnchorPaneID string
	CWD          string
	Tick         <-chan time.Time
	Now          func() time.Time
	Log          io.Writer
}

type Manager struct {
	client       ManagerClient
	config       Config
	taskID       string
	anchorPaneID string
	cwd          string
	tick         <-chan time.Time
	now          func() time.Time
	log          io.Writer
	slots        []workerSlot
	watchResults chan watchResult
	beforeClose  func()
	afterEvent   func()
}

type slotState uint8

const (
	slotEmpty slotState = iota
	slotOccupied
)

type workerSlot struct {
	number      int
	sequence    uint64
	paneID      string
	state       slotState
	startedAt   time.Time
	completedAt time.Time
	lastError   string
}

type watchResult struct {
	slotNumber int
	paneID     string
	response   transport.WaitForOutputMarkerResponse
	err        error
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

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	slots := make([]workerSlot, opts.Config.MaxWorkers)
	for i := range slots {
		slots[i].number = i + 1
	}

	return &Manager{
		client:       opts.Client,
		config:       opts.Config,
		taskID:       opts.TaskID,
		anchorPaneID: opts.AnchorPaneID,
		cwd:          opts.CWD,
		tick:         opts.Tick,
		now:          now,
		log:          log,
		slots:        slots,
		watchResults: make(chan watchResult, opts.Config.MaxWorkers),
	}, nil
}

func (m *Manager) Run(ctx context.Context) error {
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
			m.fillEmptySlots(ctx)
			m.notifyEventProcessed()
		case result := <-m.watchResults:
			m.handleWatchResult(ctx, result)
			m.notifyEventProcessed()
		}
	}
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
	m.logf("watch slot=%d pane=%s marker=%q", slot.number, paneID, m.config.Worker.CompletionMarker)
	go m.watch(ctx, slot.number, paneID)
}

func (m *Manager) watch(ctx context.Context, slotNumber int, paneID string) {
	response, err := m.client.WaitForOutputMarker(ctx, paneID, transport.WaitForOutputMarkerRequest{
		Marker: m.config.Worker.CompletionMarker,
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
	if result.response.PaneID != result.paneID || result.response.Marker != m.config.Worker.CompletionMarker {
		slot.lastError = fmt.Sprintf("unexpected watch response pane=%q marker=%q", result.response.PaneID, result.response.Marker)
		m.logf("watch rejected slot=%d pane=%s response_pane=%q marker=%q", slot.number, slot.paneID, result.response.PaneID, result.response.Marker)
		return
	}

	m.logf("close slot=%d pane=%s", slot.number, slot.paneID)
	if m.beforeClose != nil {
		m.beforeClose()
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := m.client.ClosePane(ctx, slot.paneID); err != nil {
		slot.lastError = err.Error()
		m.logf("close failed slot=%d pane=%s error=%v", slot.number, slot.paneID, err)
		return
	}

	slot.completedAt = result.response.MatchedAt
	if slot.completedAt.IsZero() {
		slot.completedAt = m.now()
	}
	m.logf("closed slot=%d pane=%s", slot.number, slot.paneID)
	slot.paneID = ""
	slot.state = slotEmpty
	slot.lastError = ""
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
	return nil
}
