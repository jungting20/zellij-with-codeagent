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
	Now      func() time.Time
}

// SubscriptionManager owns zellij subscribe processes for logical runtime panes.
type SubscriptionManager struct {
	opts SubscriptionManagerOptions

	mu             sync.Mutex
	cancelByPaneID map[registry.PaneID]context.CancelFunc
	lastRendered   map[registry.PaneID]string
}

// NewSubscriptionManager wires subscribe streaming for managed panes.
func NewSubscriptionManager(opts SubscriptionManagerOptions) *SubscriptionManager {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &SubscriptionManager{
		opts:           opts,
		cancelByPaneID: make(map[registry.PaneID]context.CancelFunc),
		lastRendered:   make(map[registry.PaneID]string),
	}
}

func (m *SubscriptionManager) takeCancel(id registry.PaneID) context.CancelFunc {
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel, ok := m.cancelByPaneID[id]
	delete(m.cancelByPaneID, id)
	delete(m.lastRendered, id)
	if !ok {
		return nil
	}
	return cancel
}

// StopPane cancels the subscribe goroutine and subprocess for a logical pane.
func (m *SubscriptionManager) StopPane(id registry.PaneID) {
	if m == nil {
		return
	}
	if cancel := m.takeCancel(id); cancel != nil {
		cancel()
	}
}

// StartPane starts zellij subscribe for the pane when bus and runner are configured.
func (m *SubscriptionManager) StartPane(logicalID registry.PaneID) {
	if m == nil || m.opts.Bus == nil || m.opts.Runner == nil || m.opts.Backend == nil || m.opts.Registry == nil {
		return
	}

	m.mu.Lock()
	if _, exists := m.cancelByPaneID[logicalID]; exists {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelByPaneID[logicalID] = cancel
	m.mu.Unlock()

	go m.run(logicalID, ctx)
}

func (m *SubscriptionManager) run(logicalID registry.PaneID, ctx context.Context) {
	defer func() {
		if c := m.takeCancel(logicalID); c != nil {
			c()
		}
	}()

	record, err := m.opts.Registry.GetPane(logicalID)
	if err != nil {
		m.publishHealth(logicalID, "subscribe skipped: registry pane unavailable")
		return
	}

	spec, err := m.opts.Backend.SubscribeCommand(zellij.SubscribeRequest{
		PaneID: zellij.PaneID(record.ZellijPaneID),
		JSON:   true,
	})
	if err != nil {
		m.publishSubscribeStartupFailure(logicalID, err)
		return
	}

	stream, err := m.opts.Runner.Start(ctx, spec)
	if err != nil {
		m.publishSubscribeStartupFailure(logicalID, err)
		return
	}
	defer stream.Stdout.Close()

	if _, err := m.opts.Registry.UpdatePaneStatus(logicalID, registry.PaneStatusRunning, "subscribe active"); err != nil {
		m.publishSubscribeStartupFailure(logicalID, err)
		return
	}

	reader := bufio.NewReader(stream.Stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			m.handleLine(logicalID, record.ZellijPaneID, string(line))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			m.publishStreamError(logicalID, err)
			break
		}
	}

	waitErr := stream.Wait()
	if waitErr != nil && ctx.Err() == nil {
		m.publishStreamError(logicalID, waitErr)
	}

	if ctx.Err() == nil && m.opts.Bus != nil {
		evt := m.baseEvent(logicalID)
		evt.Type = eventbus.TypeHealthChanged
		evt.Message = "subscribe process exited"
		m.opts.Bus.Publish(evt)
	}
}

func (m *SubscriptionManager) handleLine(logicalID registry.PaneID, zellijPane registry.ZellijPaneID, rawLine string) {
	parsed, err := ParseSubscribeNDJSONLine(rawLine)
	if err != nil {
		m.publishSubscribeParseError(logicalID, err)
		return
	}
	if parsed.Kind == ParsedSubscribeUnknown && parsed.RenderedText == "" && parsed.ZellijPaneID == "" {
		return
	}

	if parsed.ZellijPaneID != "" && string(zellijPane) != "" && parsed.ZellijPaneID != string(zellijPane) {
		return
	}

	switch parsed.Kind {
	case ParsedSubscribePaneClosed:
		m.handlePaneClosed(logicalID)
	case ParsedSubscribePaneUpdate:
		m.handlePaneUpdate(logicalID, parsed.RenderedText)
	default:
	}
}

func (m *SubscriptionManager) handlePaneClosed(logicalID registry.PaneID) {
	if m.opts.Registry == nil {
		return
	}
	if _, err := m.opts.Registry.UpdatePaneStatus(logicalID, registry.PaneStatusExited, "pane_closed event from zellij subscribe"); err != nil {
		m.publishStreamError(logicalID, err)
		return
	}

	base := m.baseEvent(logicalID)
	e := base
	e.Type = eventbus.TypePaneClosed
	e.Message = "pane_closed"
	if m.opts.Bus != nil {
		m.opts.Bus.Publish(e)
	}
}

func (m *SubscriptionManager) handlePaneUpdate(logicalID registry.PaneID, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	m.mu.Lock()
	prev := m.lastRendered[logicalID]
	if text == prev {
		m.mu.Unlock()
		return
	}
	m.lastRendered[logicalID] = text
	m.mu.Unlock()

	if _, err := m.opts.Registry.UpdatePaneOutput(logicalID, text); err != nil {
		m.publishStreamError(logicalID, err)
		return
	}

	if m.opts.Bus == nil {
		return
	}

	base := m.baseEvent(logicalID)
	raw := base
	raw.Type = eventbus.TypeRawOutput
	raw.Message = text
	m.opts.Bus.Publish(raw)

	for _, sem := range SemanticEventsFromText(text, base) {
		m.opts.Bus.Publish(sem)
	}
}

func (m *SubscriptionManager) baseEvent(logicalID registry.PaneID) eventbus.Event {
	record, err := m.opts.Registry.GetPane(logicalID)
	if err != nil {
		return eventbus.Event{
			PaneID: string(logicalID),
			Time:   m.opts.Now(),
		}
	}
	return eventbus.Event{
		PaneID:       string(record.ID),
		TaskID:       string(record.TaskID),
		AgentID:      string(record.AgentID),
		ZellijPaneID: string(record.ZellijPaneID),
		Time:         m.opts.Now(),
	}
}

func (m *SubscriptionManager) publishSubscribeStartupFailure(logicalID registry.PaneID, cause error) {
	if m.opts.Registry != nil {
		_, _ = m.opts.Registry.UpdatePaneStatus(logicalID, registry.PaneStatusError, cause.Error())
	}
	if m.opts.Bus == nil {
		return
	}
	base := m.baseEvent(logicalID)
	errEvt := base
	errEvt.Type = eventbus.TypeSubscribeError
	errEvt.Message = cause.Error()
	m.opts.Bus.Publish(errEvt)

	health := base
	health.Type = eventbus.TypeHealthChanged
	health.Message = "subscribe failed to start"
	m.opts.Bus.Publish(health)
}

func (m *SubscriptionManager) publishSubscribeParseError(logicalID registry.PaneID, cause error) {
	if m.opts.Bus == nil {
		return
	}
	base := m.baseEvent(logicalID)
	e := base
	e.Type = eventbus.TypeSubscribeError
	e.Message = cause.Error()
	m.opts.Bus.Publish(e)
}

func (m *SubscriptionManager) publishStreamError(logicalID registry.PaneID, cause error) {
	if m.opts.Bus == nil {
		return
	}
	base := m.baseEvent(logicalID)
	e := base
	e.Type = eventbus.TypeSubscribeError
	e.Message = cause.Error()
	m.opts.Bus.Publish(e)
}

func (m *SubscriptionManager) publishHealth(logicalID registry.PaneID, msg string) {
	if m.opts.Bus == nil {
		return
	}
	base := m.baseEvent(logicalID)
	e := base
	e.Type = eventbus.TypeHealthChanged
	e.Message = msg
	m.opts.Bus.Publish(e)
}
