package codingagent

import (
	"fmt"
	"sync"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/runtime"
)

const (
	startupGrace          = 3 * time.Second
	idleConfirmationDelay = 100 * time.Millisecond
	idleConfirmationCount = 3
	idleConfirmationLimit = 700 * time.Millisecond
)

// Timer is the cancelable timer surface used by Monitor scheduling.
type Timer interface {
	Stop() bool
}

type MonitorOptions struct {
	Store          Store
	Detector       *Detector
	DetectorErrors map[Kind]error
	EventBus       *eventbus.Bus
	Now            func() time.Time
	AfterFunc      func(time.Duration, func()) Timer
}

// Monitor translates runtime pane observations into coding-agent state.
type Monitor struct {
	opts MonitorOptions

	mu         sync.Mutex
	nextToken  uint64
	monitoring map[ID]*monitoredAgent
}

type monitoredAgent struct {
	record Record
	token  uint64

	latestInput DetectionInput
	hasInput    bool
	graceTimer  Timer

	idleConfirmations int
	idleTimer         Timer
	idleDeadline      Timer
}

var _ runtime.PaneObserver = (*Monitor)(nil)

func NewMonitor(opts MonitorOptions) *Monitor {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.AfterFunc == nil {
		opts.AfterFunc = func(delay time.Duration, fn func()) Timer {
			return time.AfterFunc(delay, fn)
		}
	}
	if opts.DetectorErrors != nil {
		copied := make(map[Kind]error, len(opts.DetectorErrors))
		for kind, err := range opts.DetectorErrors {
			copied[kind] = err
		}
		opts.DetectorErrors = copied
	}
	return &Monitor{opts: opts, monitoring: make(map[ID]*monitoredAgent)}
}

func (m *Monitor) Start(record Record) error {
	if err := m.opts.DetectorErrors[record.Kind]; err != nil {
		return err
	}
	if m.opts.Store == nil {
		return fmt.Errorf("coding-agent monitor store is nil")
	}
	if m.opts.Detector == nil {
		return fmt.Errorf("coding-agent monitor detector is nil")
	}
	stored, err := m.opts.Store.Get(record.ID)
	if err != nil {
		return err
	}
	if stored.Kind != record.Kind || stored.PaneID != record.PaneID {
		return fmt.Errorf("coding-agent monitor record %q does not match store", record.ID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if previous := m.monitoring[record.ID]; previous != nil {
		m.stopTimersLocked(previous)
	}
	m.nextToken++
	entry := &monitoredAgent{record: stored, token: m.nextToken}
	m.monitoring[record.ID] = entry
	token := entry.token
	entry.graceTimer = m.opts.AfterFunc(startupGrace, func() {
		m.finishGrace(record.ID, token)
	})
	return nil
}

func (m *Monitor) Stop(id ID) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.monitoring[id]
	if entry == nil {
		return
	}
	m.stopTimersLocked(entry)
	delete(m.monitoring, id)
}

func (m *Monitor) PaneOutput(pane registry.PaneRecord, renderedText string) {
	if m == nil || pane.Role != "coding-agent" || m.opts.Store == nil {
		return
	}
	record, err := m.opts.Store.GetByPane(runtime.PaneID(pane.ID))
	if err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.monitoring[record.ID]
	if !sameMonitoredRecord(entry, record) {
		return
	}
	entry.latestInput.Screen = renderedText
	entry.hasInput = true
	if entry.graceTimer != nil {
		return
	}
	m.evaluateLocked(entry)
}

func (m *Monitor) PaneClosed(pane registry.PaneRecord) {
	if m == nil || pane.Role != "coding-agent" || m.opts.Store == nil {
		return
	}
	record, err := m.opts.Store.GetByPane(runtime.PaneID(pane.ID))
	if err != nil {
		return
	}

	m.mu.Lock()
	entry := m.monitoring[record.ID]
	if !sameMonitoredRecord(entry, record) {
		m.mu.Unlock()
		return
	}
	m.stopTimersLocked(entry)
	delete(m.monitoring, record.ID)
	m.mu.Unlock()
	_ = m.opts.Store.Delete(record.ID)
}

func (m *Monitor) PaneError(pane registry.PaneRecord, cause error) {
	if m == nil || pane.Role != "coding-agent" || m.opts.Store == nil {
		return
	}
	record, err := m.opts.Store.GetByPane(runtime.PaneID(pane.ID))
	if err != nil {
		return
	}

	reason := "pane observation error"
	if cause != nil {
		reason += ": " + cause.Error()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.monitoring[record.ID]
	if !sameMonitoredRecord(entry, record) {
		return
	}
	m.cancelIdleCandidateLocked(entry)
	m.updateStateLocked(entry, StateUpdate{State: StateUnknown, Reason: reason})
}

func sameMonitoredRecord(entry *monitoredAgent, record Record) bool {
	return entry != nil && entry.record.ID == record.ID && entry.record.Kind == record.Kind && entry.record.PaneID == record.PaneID
}

func (m *Monitor) finishGrace(id ID, token uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.monitoring[id]
	if entry == nil || entry.token != token {
		return
	}
	entry.graceTimer = nil
	if entry.hasInput {
		m.evaluateLocked(entry)
	}
}

func (m *Monitor) evaluateLocked(entry *monitoredAgent) {
	detection, err := m.opts.Detector.Detect(entry.record.Kind, entry.latestInput)
	if err != nil {
		m.cancelIdleCandidateLocked(entry)
		return
	}
	if detection.SkipStateUpdate {
		m.cancelIdleCandidateLocked(entry)
		return
	}
	if entry.record.State == StateWorking && detection.State == StateIdle && !detection.VisibleIdle {
		m.startIdleCandidateLocked(entry)
		return
	}
	m.cancelIdleCandidateLocked(entry)
	m.applyDetectionLocked(entry, detection)
}

func (m *Monitor) startIdleCandidateLocked(entry *monitoredAgent) {
	if entry.idleTimer != nil || entry.idleDeadline != nil {
		return
	}
	entry.idleConfirmations = 0
	id := entry.record.ID
	token := entry.token
	entry.idleTimer = m.opts.AfterFunc(idleConfirmationDelay, func() {
		m.confirmIdle(id, token)
	})
	entry.idleDeadline = m.opts.AfterFunc(idleConfirmationLimit, func() {
		m.finishIdleDeadline(id, token)
	})
}

func (m *Monitor) confirmIdle(id ID, token uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.monitoring[id]
	if entry == nil || entry.token != token || entry.idleTimer == nil {
		return
	}
	entry.idleTimer = nil
	detection, err := m.opts.Detector.Detect(entry.record.Kind, entry.latestInput)
	if err != nil || detection.SkipStateUpdate {
		m.cancelIdleCandidateLocked(entry)
		return
	}
	if detection.State != StateIdle || detection.VisibleIdle {
		m.cancelIdleCandidateLocked(entry)
		m.applyDetectionLocked(entry, detection)
		return
	}
	entry.idleConfirmations++
	if entry.idleConfirmations >= idleConfirmationCount {
		m.cancelIdleCandidateLocked(entry)
		m.applyDetectionLocked(entry, detection)
		return
	}
	entry.idleTimer = m.opts.AfterFunc(idleConfirmationDelay, func() {
		m.confirmIdle(id, token)
	})
}

func (m *Monitor) finishIdleDeadline(id ID, token uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.monitoring[id]
	if entry == nil || entry.token != token || entry.idleDeadline == nil {
		return
	}
	entry.idleDeadline = nil
	detection, err := m.opts.Detector.Detect(entry.record.Kind, entry.latestInput)
	if err != nil || detection.SkipStateUpdate {
		m.cancelIdleCandidateLocked(entry)
		return
	}
	m.cancelIdleCandidateLocked(entry)
	m.applyDetectionLocked(entry, detection)
}

func (m *Monitor) applyDetectionLocked(entry *monitoredAgent, detection Detection) {
	m.updateStateLocked(entry, StateUpdate{
		State:       detection.State,
		Reason:      detection.Reason,
		MatchedRule: detection.RuleID,
	})
}

func (m *Monitor) updateStateLocked(entry *monitoredAgent, update StateUpdate) {
	change, err := m.opts.Store.UpdateState(entry.record.ID, update)
	if err != nil {
		return
	}
	entry.record = change.Current
	if !change.Changed || m.opts.EventBus == nil {
		return
	}
	m.opts.EventBus.Publish(eventbus.Event{
		Type:          eventbus.TypeAgentStateChanged,
		PaneID:        string(change.Current.PaneID),
		AgentID:       string(change.Current.ID),
		AgentKind:     string(change.Current.Kind),
		PreviousState: string(change.Previous.State),
		AgentState:    string(change.Current.State),
		MatchedRule:   change.Current.MatchedRule,
		Reason:        change.Current.StateReason,
		Time:          m.opts.Now(),
	})
}

func (m *Monitor) cancelIdleCandidateLocked(entry *monitoredAgent) {
	if entry.idleTimer != nil {
		entry.idleTimer.Stop()
		entry.idleTimer = nil
	}
	if entry.idleDeadline != nil {
		entry.idleDeadline.Stop()
		entry.idleDeadline = nil
	}
	entry.idleConfirmations = 0
}

func (m *Monitor) stopTimersLocked(entry *monitoredAgent) {
	if entry.graceTimer != nil {
		entry.graceTimer.Stop()
		entry.graceTimer = nil
	}
	m.cancelIdleCandidateLocked(entry)
}
