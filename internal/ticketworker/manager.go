package ticketworker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/transport"
)

type ManagerStore interface {
	Next(context.Context) (Ticket, error)
	Transition(context.Context, int64, Action) (Ticket, error)
	Requeue(context.Context, int64) (Ticket, error)
}

type ManagerClient interface {
	CreatePane(context.Context, transport.CreatePaneRequest) (transport.CreatePaneResponse, error)
	SendInput(context.Context, string, transport.SendInputRequest) error
	SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error)
	ClosePane(context.Context, string) (transport.ClosePaneResponse, error)
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
}

type ManagerOptions struct {
	Store             ManagerStore
	Client            ManagerClient
	Config            Config
	Root              string
	TaskID            string
	AnchorPaneID      string
	ZellijSession     string
	RoleBin           string
	StartupTimeout    time.Duration
	PollInterval      time.Duration
	ReadyPollInterval time.Duration
	Tick              <-chan time.Time
	Log               io.Writer
	ManagerID         string
}

type managerSlotState uint8

const (
	managerSlotEmpty managerSlotState = iota
	managerSlotStarting
	managerSlotWorking
	managerSlotCompleting
	managerSlotClosing
	managerSlotCleanupFailed
)

type managerSlot struct {
	state             managerSlotState
	ticket            Ticket
	paneID            string
	marker            string
	prompt            string
	createRequest     transport.CreatePaneRequest
	paneCreated       bool
	creationUncertain bool
	done              bool
	lastError         error
}

type Manager struct {
	store             ManagerStore
	client            ManagerClient
	config            Config
	root              string
	taskID            string
	anchorPaneID      string
	zellijSession     string
	roleBin           string
	startupTimeout    time.Duration
	pollInterval      time.Duration
	readyPollInterval time.Duration
	tick              <-chan time.Time
	log               io.Writer
	managerID         string
	slots             []managerSlot
	stream            *transport.EventStream
}

func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("ticket manager store is required")
	}
	if opts.Client == nil {
		return nil, fmt.Errorf("ticket manager client is required")
	}
	if err := validateConfig(opts.Config); err != nil {
		return nil, fmt.Errorf("ticket manager config: %w", err)
	}
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return nil, fmt.Errorf("ticket manager root is required")
	}
	taskID := strings.TrimSpace(opts.TaskID)
	if taskID == "" {
		return nil, fmt.Errorf("ticket manager task ID is required")
	}
	anchorPaneID := strings.TrimSpace(opts.AnchorPaneID)
	if anchorPaneID == "" {
		return nil, fmt.Errorf("ticket manager anchor pane ID is required")
	}
	zellijSession := strings.TrimSpace(opts.ZellijSession)
	if zellijSession == "" {
		return nil, fmt.Errorf("ticket manager Zellij session is required")
	}
	roleBin := strings.TrimSpace(opts.RoleBin)
	if roleBin == "" {
		return nil, fmt.Errorf("ticket manager role executable is required")
	}
	startupTimeout := opts.StartupTimeout
	if startupTimeout == 0 {
		startupTimeout = 15 * time.Second
	}
	if startupTimeout < 0 {
		return nil, fmt.Errorf("ticket manager startup timeout must be positive")
	}
	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = opts.Config.PollInterval
	}
	if pollInterval <= 0 {
		return nil, fmt.Errorf("ticket manager poll interval must be positive")
	}
	readyPollInterval := opts.ReadyPollInterval
	if readyPollInterval == 0 {
		readyPollInterval = 50 * time.Millisecond
	}
	if readyPollInterval < 0 {
		return nil, fmt.Errorf("ticket manager ready poll interval must be positive")
	}
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	managerID := strings.TrimSpace(opts.ManagerID)
	if managerID == "" {
		generated, err := newManagerID()
		if err != nil {
			return nil, err
		}
		managerID = generated
	}
	if !validManagerID(managerID) {
		return nil, fmt.Errorf("ticket manager ID must contain only letters, digits, hyphens, or underscores")
	}
	return &Manager{
		store: opts.Store, client: opts.Client, config: opts.Config,
		root: root, taskID: taskID, anchorPaneID: anchorPaneID, zellijSession: zellijSession, roleBin: roleBin,
		startupTimeout: startupTimeout, pollInterval: pollInterval, readyPollInterval: readyPollInterval,
		tick: opts.Tick, log: log, managerID: managerID, slots: make([]managerSlot, opts.Config.MaxWorkers),
	}, nil
}

func newManagerID() (string, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate ticket manager ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func validManagerID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (m *Manager) Run(ctx context.Context) error {
	if err := m.waitForAnchor(ctx); err != nil {
		return err
	}
	if err := m.connectStream(ctx); err != nil {
		return fmt.Errorf("connect ticket manager event stream: %w", err)
	}
	m.fillEmptySlots(ctx)

	ticks := m.tick
	var ticker *time.Ticker
	if ticks == nil {
		ticker = time.NewTicker(m.pollInterval)
		defer ticker.Stop()
		ticks = ticker.C
	}

	for {
		var events <-chan transport.Event
		var streamErrors <-chan error
		if m.stream != nil {
			events = m.stream.Events
			streamErrors = m.stream.Errors
		}
		select {
		case <-ctx.Done():
			return m.shutdown()
		case <-ticks:
			m.retrySlots(ctx)
			if m.stream == nil {
				if err := m.connectStream(ctx); err != nil {
					m.logf("event stream reconnect failed: %v", err)
					continue
				}
			}
			m.recoverSnapshots(ctx)
			m.fillEmptySlots(ctx)
		case event, ok := <-events:
			if !ok {
				m.disconnectStream()
				continue
			}
			m.handleEvent(ctx, event)
		case err, ok := <-streamErrors:
			if ok && err != nil {
				m.logf("event stream lost: %v", err)
			}
			m.disconnectStream()
		}
	}
}

func (m *Manager) waitForAnchor(ctx context.Context) error {
	readyCtx, cancel := context.WithTimeout(ctx, m.startupTimeout)
	defer cancel()
	ticker := time.NewTicker(m.readyPollInterval)
	defer ticker.Stop()
	for {
		response, err := m.client.InspectRuntime(readyCtx)
		if err == nil && m.hasReadyAnchor(response, m.anchorPaneID) {
			return nil
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("ticket manager anchor %q not ready: %w", m.anchorPaneID, readyCtx.Err())
		case <-ticker.C:
		}
	}
}

func (m *Manager) hasReadyAnchor(response transport.InspectRuntimeResponse, paneID string) bool {
	for _, pane := range response.Panes {
		if pane.ID == paneID && pane.TaskID == m.taskID && pane.SessionID == m.zellijSession && (pane.Status == "starting" || pane.Status == "running") {
			return true
		}
	}
	return false
}

func (m *Manager) connectStream(ctx context.Context) error {
	stream, err := m.client.StreamEvents(ctx)
	if err != nil {
		return err
	}
	if stream == nil || stream.Events == nil || stream.Errors == nil {
		if stream != nil && stream.Close != nil {
			_ = stream.Close()
		}
		return fmt.Errorf("runtime returned an invalid event stream")
	}
	m.stream = stream
	return nil
}

func (m *Manager) disconnectStream() {
	if m.stream != nil && m.stream.Close != nil {
		_ = m.stream.Close()
	}
	m.stream = nil
}

func (m *Manager) fillEmptySlots(ctx context.Context) {
	if m.stream == nil {
		return
	}
	for i := range m.slots {
		if ctx.Err() != nil {
			return
		}
		if m.slots[i].state != managerSlotEmpty {
			continue
		}
		stop := m.startSlot(ctx, &m.slots[i])
		if stop {
			return
		}
	}
}

func (m *Manager) startSlot(ctx context.Context, slot *managerSlot) bool {
	ticket, err := m.store.Next(ctx)
	if errors.Is(err, ErrEmptyQueue) {
		return true
	}
	if err != nil {
		m.logf("claim ticket failed: %v", err)
		return true
	}
	slot.state = managerSlotStarting
	slot.ticket = ticket
	slot.paneID = "ticket-coding-" + m.managerID + "-" + strconv.FormatInt(ticket.ID, 10)
	slot.prompt, slot.marker, err = RenderTicketPrompt(ticket)
	if err != nil {
		m.logf("render ticket=%d failed: %v", ticket.ID, err)
		m.requeueWithoutPane(ctx, slot)
		return true
	}

	req := transport.CreatePaneRequest{
		ID: slot.paneID, TaskID: m.taskID, ZellijSession: m.zellijSession,
		Role: "coding-agent", Name: slot.paneID, SameTabAsPaneID: m.anchorPaneID,
		Command: []string{m.roleBin, "role", "coding-agent", "--yolo", m.root}, CWD: m.root,
	}
	slot.createRequest = req
	if _, err := m.client.CreatePane(ctx, req); err != nil {
		m.logf("create ticket=%d pane=%s failed: %v", ticket.ID, slot.paneID, err)
		if safeCreateFailure(err) {
			m.requeueWithoutPane(ctx, slot)
		} else {
			slot.state = managerSlotCleanupFailed
			slot.creationUncertain = true
			slot.lastError = err
		}
		return true
	}
	slot.paneCreated = true
	if err := m.waitForInputReady(ctx, slot.paneID); err != nil {
		slot.lastError = err
		slot.state = managerSlotCleanupFailed
		m.retryCleanup(ctx, slot)
		return true
	}
	if err := m.client.SendInput(ctx, slot.paneID, transport.SendInputRequest{Text: slot.prompt + "\n"}); err != nil {
		slot.lastError = err
		slot.state = managerSlotCleanupFailed
		m.retryCleanup(ctx, slot)
		return true
	}
	slot.state = managerSlotWorking
	m.logf("started ticket=%d pane=%s", ticket.ID, slot.paneID)
	return false
}

func safeCreateFailure(err error) bool {
	var clientErr *transport.ClientError
	if !errors.As(err, &clientErr) {
		return false
	}
	return clientErr.APIError.Code == transport.CodeBadRequest || clientErr.APIError.Code == transport.CodeNotFound
}

func (m *Manager) waitForInputReady(ctx context.Context, paneID string) error {
	readyCtx, cancel := context.WithTimeout(ctx, m.startupTimeout)
	defer cancel()
	ticker := time.NewTicker(m.readyPollInterval)
	defer ticker.Stop()
	for {
		response, err := m.client.SnapshotOutput(readyCtx, paneID, transport.SnapshotOutputRequest{})
		if err == nil && strings.Contains(response.Output, "›") {
			return nil
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("wait for coding-agent prompt pane=%s: %w", paneID, readyCtx.Err())
		case <-ticker.C:
		}
	}
}

func (m *Manager) requeueWithoutPane(ctx context.Context, slot *managerSlot) {
	if _, err := m.store.Requeue(ctx, slot.ticket.ID); err != nil {
		slot.state = managerSlotCleanupFailed
		slot.lastError = err
		return
	}
	*slot = managerSlot{}
}

func (m *Manager) handleEvent(ctx context.Context, event transport.Event) {
	if event.Type != string(eventbus.TypeRawOutput) {
		return
	}
	if event.TaskID != m.taskID {
		return
	}
	for i := range m.slots {
		slot := &m.slots[i]
		if slot.state != managerSlotWorking || slot.paneID != event.PaneID {
			continue
		}
		if containsExactLine(event.Message, slot.marker) && m.workerExists(ctx, slot) {
			m.completeSlot(ctx, slot)
		}
		return
	}
}

func (m *Manager) workerExists(ctx context.Context, slot *managerSlot) bool {
	response, err := m.client.InspectRuntime(ctx)
	if err != nil {
		m.logf("inspect worker pane=%s failed: %v", slot.paneID, err)
		return false
	}
	for _, pane := range response.Panes {
		if m.matchesWorkerPane(pane, slot, false) {
			return true
		}
	}
	return false
}

func (m *Manager) matchesWorkerPane(pane transport.Pane, slot *managerSlot, requireActive bool) bool {
	if pane.ID != slot.paneID || pane.TaskID != m.taskID || pane.SessionID != m.zellijSession || pane.Role != "coding-agent" {
		return false
	}
	return !requireActive || pane.Status == "starting" || pane.Status == "running"
}

func (m *Manager) completeSlot(ctx context.Context, slot *managerSlot) {
	if slot.state != managerSlotWorking {
		return
	}
	slot.state = managerSlotCompleting
	m.retryComplete(ctx, slot)
}

func (m *Manager) retrySlots(ctx context.Context) {
	for i := range m.slots {
		switch m.slots[i].state {
		case managerSlotCompleting:
			m.retryComplete(ctx, &m.slots[i])
		case managerSlotClosing:
			m.retryClose(ctx, &m.slots[i])
		case managerSlotCleanupFailed:
			m.retryCleanup(ctx, &m.slots[i])
		}
	}
}

func (m *Manager) retryComplete(ctx context.Context, slot *managerSlot) {
	if _, err := m.store.Transition(ctx, slot.ticket.ID, ActionDone); err != nil {
		slot.lastError = err
		m.logf("complete ticket=%d failed: %v", slot.ticket.ID, err)
		return
	}
	slot.done = true
	slot.state = managerSlotClosing
	m.retryClose(ctx, slot)
}

func (m *Manager) retryClose(ctx context.Context, slot *managerSlot) {
	closed, err := m.closeOrAbsent(ctx, slot.paneID)
	if !closed {
		slot.lastError = err
		m.logf("close ticket=%d pane=%s failed: %v", slot.ticket.ID, slot.paneID, err)
		return
	}
	m.logf("closed ticket=%d pane=%s", slot.ticket.ID, slot.paneID)
	*slot = managerSlot{}
}

func (m *Manager) retryCleanup(ctx context.Context, slot *managerSlot) {
	if slot.creationUncertain {
		if !m.resolveUncertainCreation(ctx, slot) {
			return
		}
	}
	if slot.paneCreated {
		closed, err := m.closeOrAbsent(ctx, slot.paneID)
		if !closed {
			slot.lastError = err
			return
		}
		slot.paneCreated = false
	}
	if _, err := m.store.Requeue(ctx, slot.ticket.ID); err != nil {
		slot.lastError = err
		return
	}
	*slot = managerSlot{}
}

func (m *Manager) resolveUncertainCreation(ctx context.Context, slot *managerSlot) bool {
	response, err := m.client.InspectRuntime(ctx)
	if err != nil {
		slot.lastError = err
		return false
	}
	for _, pane := range response.Panes {
		if m.matchesWorkerPane(pane, slot, false) {
			slot.creationUncertain = false
			slot.paneCreated = true
			return true
		}
	}
	if _, err := m.client.CreatePane(ctx, slot.createRequest); err == nil {
		slot.creationUncertain = false
		slot.paneCreated = true
		return true
	} else {
		slot.lastError = err
	}
	response, err = m.client.InspectRuntime(ctx)
	if err != nil {
		slot.lastError = err
		return false
	}
	for _, pane := range response.Panes {
		if m.matchesWorkerPane(pane, slot, false) {
			slot.creationUncertain = false
			slot.paneCreated = true
			return true
		}
	}
	return false
}

func (m *Manager) closeOrAbsent(ctx context.Context, paneID string) (bool, error) {
	if _, err := m.client.ClosePane(ctx, paneID); err == nil {
		return true, nil
	} else {
		response, inspectErr := m.client.InspectRuntime(ctx)
		if inspectErr != nil {
			return false, errors.Join(err, inspectErr)
		}
		if !m.paneConsumesCapacity(response, paneID) {
			return true, nil
		}
		return false, err
	}
}

func (m *Manager) paneConsumesCapacity(response transport.InspectRuntimeResponse, paneID string) bool {
	for _, pane := range response.Panes {
		if pane.ID != paneID || pane.TaskID != m.taskID || pane.SessionID != m.zellijSession {
			continue
		}
		switch pane.Status {
		case "closed", "exited", "lost":
			return false
		default:
			return true
		}
	}
	return false
}

func (m *Manager) recoverSnapshots(ctx context.Context) {
	for i := range m.slots {
		slot := &m.slots[i]
		if slot.state != managerSlotWorking {
			continue
		}
		response, err := m.client.SnapshotOutput(ctx, slot.paneID, transport.SnapshotOutputRequest{})
		if err != nil {
			m.logf("snapshot recovery pane=%s failed: %v", slot.paneID, err)
			continue
		}
		if m.matchesWorkerPane(response.Pane, slot, true) && containsExactLine(response.Output, slot.marker) {
			m.completeSlot(ctx, slot)
		}
	}
}

func (m *Manager) shutdown() error {
	m.disconnectStream()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), m.startupTimeout)
	defer cancel()
	var cleanupErrors []error
	for i := range m.slots {
		slot := &m.slots[i]
		if slot.state == managerSlotEmpty {
			continue
		}
		if slot.creationUncertain && !m.resolveUncertainCreation(cleanupCtx, slot) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("ticket %d pane creation outcome is unknown", slot.ticket.ID))
			continue
		}
		if slot.paneCreated {
			closed, err := m.closeOrAbsent(cleanupCtx, slot.paneID)
			if !closed {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("close ticket %d: %w", slot.ticket.ID, err))
				continue
			}
		}
		if !slot.done {
			if _, err := m.store.Requeue(cleanupCtx, slot.ticket.ID); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("requeue ticket %d: %w", slot.ticket.ID, err))
				continue
			}
		}
		*slot = managerSlot{}
	}
	return errors.Join(cleanupErrors...)
}

func (m *Manager) logf(format string, args ...any) {
	fmt.Fprintf(m.log, format+"\n", args...)
}
