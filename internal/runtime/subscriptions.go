package runtime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

// SubscriptionStream is stdout from a long-running zellij subscribe process.
type SubscriptionStream struct {
	Stdout io.ReadCloser
	Wait   func() error
}

// SubscriptionRunner starts subscribe subprocesses owned by the daemon.
type SubscriptionRunner interface {
	Start(ctx context.Context, spec zellij.CommandSpec) (*SubscriptionStream, error)
}

// PaneObserver receives daemon-owned pane observations after generation checks.
type PaneObserver interface {
	PaneOutput(registry.PaneRecord, string)
	PaneClosed(registry.PaneRecord)
	PaneError(registry.PaneRecord, error)
}

// ExecSubscriptionRunner runs subscribe commands with exec.CommandContext.
type ExecSubscriptionRunner struct{}

func (ExecSubscriptionRunner) Start(ctx context.Context, spec zellij.CommandSpec) (*SubscriptionStream, error) {
	if spec.Name == "" {
		return nil, errors.New("empty subscribe command name")
	}
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &SubscriptionStream{
		Stdout: stdout,
		Wait:   cmd.Wait,
	}, nil
}

// SubscriptionManagerOptions configures pane subscribe lifecycle wiring.
type SubscriptionManagerOptions struct {
	Registry *registry.Registry
	Backend  zellij.Backend
	Bus      *eventbus.Bus
	Runner   SubscriptionRunner
	Observer PaneObserver
	Now      func() time.Time
}

// SubscriptionManager owns zellij subscribe processes for logical runtime panes.
type SubscriptionManager struct {
	opts SubscriptionManagerOptions

	mu             sync.Mutex
	cancelByPaneID map[registry.PaneID]*paneSubscription
	lastRendered   map[subscriptionKey]string
}

type paneSubscription struct {
	cancel context.CancelFunc
	ctx    context.Context
	done   chan struct{}
	key    subscriptionKey
}

type subscriptionKey struct {
	paneID     registry.PaneID
	generation uint64
}

// NewSubscriptionManager wires subscribe streaming for managed panes.
func NewSubscriptionManager(opts SubscriptionManagerOptions) *SubscriptionManager {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &SubscriptionManager{
		opts:           opts,
		cancelByPaneID: make(map[registry.PaneID]*paneSubscription),
		lastRendered:   make(map[subscriptionKey]string),
	}
}

func (m *SubscriptionManager) takeSubscription(id registry.PaneID, expected *paneSubscription) *paneSubscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.cancelByPaneID[id]
	if !ok || (expected != nil && current != expected) {
		return nil
	}
	delete(m.cancelByPaneID, id)
	delete(m.lastRendered, current.key)
	return current
}

// StopPane cancels the subscribe goroutine and subprocess for a logical pane.
func (m *SubscriptionManager) StopPane(id registry.PaneID) {
	m.stopPaneGeneration(id, 0)
}

// StopPaneGeneration cancels only the subscription that belongs to the
// expected registry generation. A replacement pane with the same ID is kept.
func (m *SubscriptionManager) StopPaneGeneration(id registry.PaneID, generation uint64) {
	m.stopPaneGeneration(id, generation)
}

func (m *SubscriptionManager) stopPaneGeneration(id registry.PaneID, generation uint64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	subscription := m.cancelByPaneID[id]
	if subscription == nil || (generation != 0 && subscription.key.generation != generation) {
		m.mu.Unlock()
		return
	}
	delete(m.cancelByPaneID, id)
	delete(m.lastRendered, subscription.key)
	m.mu.Unlock()
	if subscription != nil {
		subscription.cancel()
	}
}

// StartPane starts zellij subscribe for the pane when bus and runner are configured.
func (m *SubscriptionManager) StartPane(logicalID registry.PaneID) {
	if m == nil || m.opts.Bus == nil || m.opts.Runner == nil || m.opts.Backend == nil || m.opts.Registry == nil {
		return
	}
	for {
		record, err := m.opts.Registry.GetPane(logicalID)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				return
			}
			m.publishHealthForID(logicalID, "subscribe skipped: registry pane unavailable")
			return
		}
		if !m.startRecord(record) {
			return
		}
	}
}

// startRecord installs a subscription for the captured registry generation.
// It asks the caller to retry when that generation changed during installation.
func (m *SubscriptionManager) startRecord(record registry.PaneRecord) bool {
	ctx, cancel := context.WithCancel(context.Background())
	subscription := &paneSubscription{
		cancel: cancel,
		ctx:    ctx,
		done:   make(chan struct{}),
		key: subscriptionKey{
			paneID:     record.ID,
			generation: record.Generation,
		},
	}

	m.mu.Lock()
	existing := m.cancelByPaneID[record.ID]
	if existing != nil && existing.key.generation == record.Generation {
		m.mu.Unlock()
		cancel()
		close(subscription.done)
		return false
	}
	if existing != nil && existing.key.generation > record.Generation {
		m.mu.Unlock()
		cancel()
		close(subscription.done)
		return true
	}
	m.cancelByPaneID[record.ID] = subscription
	m.mu.Unlock()

	if existing != nil {
		existing.cancel()
	}

	current, err := m.opts.Registry.GetPane(record.ID)
	if err != nil || current.Generation != record.Generation {
		if owned := m.takeSubscription(record.ID, subscription); owned != nil {
			owned.cancel()
		} else {
			subscription.cancel()
		}
		close(subscription.done)
		return err == nil
	}

	go m.run(record, subscription, ctx)
	return false
}

func (m *SubscriptionManager) subscriptionIsCurrent(record registry.PaneRecord, expected *paneSubscription) bool {
	if expected == nil || expected.ctx.Err() != nil {
		return false
	}

	m.mu.Lock()
	currentSubscription := m.cancelByPaneID[record.ID]
	m.mu.Unlock()
	if currentSubscription != expected {
		return false
	}

	currentRecord, err := m.opts.Registry.GetPane(record.ID)
	return err == nil && currentRecord.Generation == record.Generation
}

func (m *SubscriptionManager) run(record registry.PaneRecord, subscription *paneSubscription, ctx context.Context) {
	logicalID := record.ID
	defer func() {
		defer close(subscription.done)
		if owned := m.takeSubscription(logicalID, subscription); owned != nil {
			owned.cancel()
		}
	}()

	spec, err := m.opts.Backend.SubscribeCommand(zellij.SubscribeRequest{
		Session: string(record.SessionID),
		PaneID:  zellij.PaneID(record.ZellijPaneID),
		JSON:    true,
	})
	if err != nil {
		if m.subscriptionIsCurrent(record, subscription) {
			m.publishSubscribeStartupFailure(record, err)
		}
		return
	}

	stream, err := m.opts.Runner.Start(ctx, spec)
	if err != nil {
		if m.subscriptionIsCurrent(record, subscription) {
			m.publishSubscribeStartupFailure(record, err)
		}
		return
	}
	defer stream.Stdout.Close()

	if _, err := m.opts.Registry.UpdatePaneStatusGeneration(logicalID, record.Generation, registry.PaneStatusRunning, "subscribe active"); err != nil {
		if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrStaleRecord) {
			return
		}
		if m.subscriptionIsCurrent(record, subscription) {
			m.publishSubscribeStartupFailure(record, err)
		}
		return
	}

	reader := bufio.NewReader(stream.Stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			m.handleLine(record, subscription, string(line))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if m.subscriptionIsCurrent(record, subscription) {
				m.publishStreamError(record, err)
			}
			break
		}
	}

	waitErr := stream.Wait()
	if waitErr != nil && m.subscriptionIsCurrent(record, subscription) {
		m.publishStreamError(record, waitErr)
	}

	if m.opts.Bus != nil && m.subscriptionIsCurrent(record, subscription) {
		evt := eventFromRecord(record, m.opts.Now())
		evt.Type = eventbus.TypeHealthChanged
		evt.Message = "subscribe process exited"
		m.opts.Bus.Publish(evt)
	}
}

func (m *SubscriptionManager) handleLine(record registry.PaneRecord, subscription *paneSubscription, rawLine string) {
	if !m.subscriptionIsCurrent(record, subscription) {
		return
	}
	parsed, err := ParseSubscribeNDJSONLine(rawLine)
	if err != nil {
		m.publishSubscribeParseError(record, err)
		return
	}
	if parsed.Kind == ParsedSubscribeUnknown && parsed.RenderedText == "" && parsed.ZellijPaneID == "" {
		return
	}

	if parsed.ZellijPaneID != "" && record.ZellijPaneID != "" && parsed.ZellijPaneID != string(record.ZellijPaneID) {
		return
	}

	switch parsed.Kind {
	case ParsedSubscribePaneClosed:
		if owned := m.takeSubscription(record.ID, subscription); owned != nil {
			owned.cancel()
		}
		m.handlePaneClosed(record)
	case ParsedSubscribePaneUpdate:
		m.handlePaneUpdate(record, parsed.RenderedText)
	default:
	}
}

func (m *SubscriptionManager) handlePaneClosed(record registry.PaneRecord) {
	if m.opts.Registry == nil {
		return
	}
	removed, err := m.opts.Registry.RemovePaneGeneration(record.ID, record.Generation)
	if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrStaleRecord) {
		return
	}
	if err != nil {
		m.publishStreamError(record, err)
		return
	}

	base := eventFromRecord(removed, m.opts.Now())
	e := base
	e.Type = eventbus.TypePaneClosed
	e.Message = "pane_closed"
	if m.opts.Observer != nil {
		m.opts.Observer.PaneClosed(removed)
	}
	if m.opts.Bus != nil {
		m.opts.Bus.Publish(e)
	}
}

func (m *SubscriptionManager) handlePaneUpdate(record registry.PaneRecord, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	key := subscriptionKey{paneID: record.ID, generation: record.Generation}
	m.mu.Lock()
	prev := m.lastRendered[key]
	if text == prev {
		m.mu.Unlock()
		return
	}
	m.lastRendered[key] = text
	m.mu.Unlock()

	updated, err := m.opts.Registry.UpdatePaneOutputGeneration(record.ID, record.Generation, text)
	if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrStaleRecord) {
		return
	}
	if err != nil {
		m.publishStreamError(record, err)
		return
	}
	if m.opts.Observer != nil {
		m.opts.Observer.PaneOutput(updated, text)
	}

	if m.opts.Bus == nil {
		return
	}

	base := eventFromRecord(updated, m.opts.Now())
	raw := base
	raw.Type = eventbus.TypeRawOutput
	raw.Message = text
	m.opts.Bus.Publish(raw)

	for _, sem := range SemanticEventsFromText(text, base) {
		m.opts.Bus.Publish(sem)
	}
}

func eventFromRecord(record registry.PaneRecord, now time.Time) eventbus.Event {
	return eventbus.Event{
		PaneID:       string(record.ID),
		TaskID:       string(record.TaskID),
		AgentID:      string(record.AgentID),
		ZellijPaneID: string(record.ZellijPaneID),
		Time:         now,
	}
}

func (m *SubscriptionManager) publishSubscribeStartupFailure(record registry.PaneRecord, cause error) {
	if m.opts.Registry != nil {
		_, _ = m.opts.Registry.UpdatePaneStatusGeneration(record.ID, record.Generation, registry.PaneStatusError, cause.Error())
	}
	if m.opts.Observer != nil {
		m.opts.Observer.PaneError(record, cause)
	}
	if m.opts.Bus == nil {
		return
	}
	base := eventFromRecord(record, m.opts.Now())
	errEvt := base
	errEvt.Type = eventbus.TypeSubscribeError
	errEvt.Message = cause.Error()
	m.opts.Bus.Publish(errEvt)

	health := base
	health.Type = eventbus.TypeHealthChanged
	health.Message = "subscribe failed to start"
	m.opts.Bus.Publish(health)
}

func (m *SubscriptionManager) publishSubscribeParseError(record registry.PaneRecord, cause error) {
	if m.opts.Observer != nil {
		m.opts.Observer.PaneError(record, cause)
	}
	if m.opts.Bus == nil {
		return
	}
	base := eventFromRecord(record, m.opts.Now())
	e := base
	e.Type = eventbus.TypeSubscribeError
	e.Message = cause.Error()
	m.opts.Bus.Publish(e)
}

func (m *SubscriptionManager) publishStreamError(record registry.PaneRecord, cause error) {
	if m.opts.Observer != nil {
		m.opts.Observer.PaneError(record, cause)
	}
	if m.opts.Bus == nil {
		return
	}
	base := eventFromRecord(record, m.opts.Now())
	e := base
	e.Type = eventbus.TypeSubscribeError
	e.Message = cause.Error()
	m.opts.Bus.Publish(e)
}

func (m *SubscriptionManager) publishHealthForID(logicalID registry.PaneID, msg string) {
	if m.opts.Bus == nil {
		return
	}
	m.opts.Bus.Publish(eventbus.Event{
		Type:    eventbus.TypeHealthChanged,
		PaneID:  string(logicalID),
		Message: msg,
		Time:    m.opts.Now(),
	})
}
