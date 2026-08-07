package daemoncli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/voice"
)

func TestAgentIdleVoiceEnqueuesEnabledTransitions(t *testing.T) {
	for _, previous := range []codingagent.State{codingagent.StateWorking, codingagent.StateBlocked} {
		t.Run(string(previous), func(t *testing.T) {
			store := newAgentIdleVoiceTestStore(codingagent.Record{
				ID: "agent-3", Kind: codingagent.KindCodex, PaneID: "agent-3",
				CWD:   "/workspace/sample-project",
				State: codingagent.StateIdle, NotifyOnIdle: true,
				StateChangedAt: time.Unix(2, 123),
			})
			queue := &agentIdleVoiceTestQueue{}

			handleAgentIdleVoiceEvent(eventbus.Event{
				Type: eventbus.TypeAgentStateChanged, AgentID: "agent-3",
				PreviousState: string(previous), AgentState: string(codingagent.StateIdle),
			}, store, queue, &bytes.Buffer{})

			want := []voice.Notification{{
				RequestID: "agent-idle:agent-3:2000000123",
				Message:   "sample-project 작업이 완료되었습니다",
			}}
			if got := queue.snapshot(); !reflect.DeepEqual(got, want) {
				t.Fatalf("notifications = %#v, want %#v", got, want)
			}
		})
	}
}

func TestAgentIdleVoiceIgnoresInitialStateDetection(t *testing.T) {
	for _, state := range []codingagent.State{codingagent.StateWorking, codingagent.StateIdle} {
		t.Run(string(state), func(t *testing.T) {
			store := newAgentIdleVoiceTestStore(codingagent.Record{
				ID: "agent-3", Kind: codingagent.KindCodex, PaneID: "agent-3",
				State: state, NotifyOnIdle: true,
				StateChangedAt: time.Unix(2, 123),
			})
			queue := &agentIdleVoiceTestQueue{}

			handleAgentIdleVoiceEvent(eventbus.Event{
				Type: eventbus.TypeAgentStateChanged, AgentID: "agent-3",
				PreviousState: string(codingagent.StateUnknown), AgentState: string(state),
			}, store, queue, &bytes.Buffer{})

			if got := queue.snapshot(); len(got) != 0 {
				t.Fatalf("notifications = %#v, want none", got)
			}
		})
	}
}

func TestAgentIdleVoiceFiltersIneligibleEvents(t *testing.T) {
	enabled := codingagent.Record{
		ID: "agent-3", Kind: codingagent.KindCodex, PaneID: "agent-3",
		State: codingagent.StateIdle, NotifyOnIdle: true,
		StateChangedAt: time.Unix(2, 123),
	}
	tests := []struct {
		name  string
		event eventbus.Event
		store *agentIdleVoiceTestStore
	}{
		{name: "idle to idle", event: idleVoiceTestEvent("agent-3", codingagent.StateIdle), store: newAgentIdleVoiceTestStore(enabled)},
		{
			name: "destination working",
			event: eventbus.Event{
				Type: eventbus.TypeAgentStateChanged, AgentID: "agent-3",
				PreviousState: string(codingagent.StateIdle), AgentState: string(codingagent.StateWorking),
			},
			store: newAgentIdleVoiceTestStore(enabled),
		},
		{name: "unrelated event", event: eventbus.Event{Type: eventbus.TypeRawOutput, AgentID: "agent-3"}, store: newAgentIdleVoiceTestStore(enabled)},
		{name: "empty agent id", event: idleVoiceTestEvent("", codingagent.StateWorking), store: newAgentIdleVoiceTestStore(enabled)},
		{
			name:  "disabled record",
			event: idleVoiceTestEvent("agent-3", codingagent.StateWorking),
			store: newAgentIdleVoiceTestStore(codingagent.Record{
				ID: "agent-3", Kind: codingagent.KindCodex, PaneID: "agent-3",
				State: codingagent.StateIdle, StateChangedAt: time.Unix(2, 123),
			}),
		},
		{name: "missing record", event: idleVoiceTestEvent("missing", codingagent.StateWorking), store: newAgentIdleVoiceTestStore()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := &agentIdleVoiceTestQueue{}
			var log bytes.Buffer

			handleAgentIdleVoiceEvent(tt.event, tt.store, queue, &log)

			if got := queue.snapshot(); len(got) != 0 {
				t.Fatalf("notifications = %#v, want none", got)
			}
			if log.Len() != 0 {
				t.Fatalf("log = %q, want empty", log.String())
			}
		})
	}
}

func TestAgentIdleVoiceLogsLookupFailure(t *testing.T) {
	store := &agentIdleVoiceTestStore{err: errors.New("store unavailable")}
	queue := &agentIdleVoiceTestQueue{}
	var log bytes.Buffer

	handleAgentIdleVoiceEvent(idleVoiceTestEvent("agent-3", codingagent.StateWorking), store, queue, &log)

	if got := queue.snapshot(); len(got) != 0 {
		t.Fatalf("notifications = %#v, want none", got)
	}
	if !strings.Contains(log.String(), "agent-3") || !strings.Contains(log.String(), "store unavailable") {
		t.Fatalf("log = %q, want agent and lookup error", log.String())
	}
}

func TestAgentIdleVoiceLoopContinuesAfterEnqueueFailure(t *testing.T) {
	store := newAgentIdleVoiceTestStore(codingagent.Record{
		ID: "agent-3", Kind: codingagent.KindCodex, PaneID: "agent-3",
		State: codingagent.StateIdle, NotifyOnIdle: true,
		StateChangedAt: time.Unix(2, 123),
	})
	queue := &agentIdleVoiceTestQueue{errors: []error{errors.New("queue full"), nil}}
	events := make(chan eventbus.Event, 2)
	events <- idleVoiceTestEvent("agent-3", codingagent.StateWorking)
	events <- idleVoiceTestEvent("agent-3", codingagent.StateBlocked)
	close(events)
	var log bytes.Buffer

	runAgentIdleVoiceLoop(context.Background(), events, store, queue, &log)

	if got := len(queue.snapshot()); got != 2 {
		t.Fatalf("enqueue calls = %d, want 2", got)
	}
	if !strings.Contains(log.String(), "agent-3") || !strings.Contains(log.String(), "queue full") {
		t.Fatalf("log = %q, want first enqueue failure", log.String())
	}
}

func TestAgentIdleVoiceLoopStopsOnCancellationAndClosedChannel(t *testing.T) {
	t.Run("closed channel", func(t *testing.T) {
		events := make(chan eventbus.Event)
		close(events)
		done := make(chan struct{})
		go func() {
			runAgentIdleVoiceLoop(context.Background(), events, newAgentIdleVoiceTestStore(), &agentIdleVoiceTestQueue{}, &bytes.Buffer{})
			close(done)
		}()
		waitAgentIdleVoiceDone(t, done)
	})
	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan eventbus.Event)
		done := make(chan struct{})
		go func() {
			runAgentIdleVoiceLoop(ctx, events, newAgentIdleVoiceTestStore(), &agentIdleVoiceTestQueue{}, &bytes.Buffer{})
			close(done)
		}()
		cancel()
		waitAgentIdleVoiceDone(t, done)
	})
}

func idleVoiceTestEvent(agentID string, previous codingagent.State) eventbus.Event {
	return eventbus.Event{
		Type: eventbus.TypeAgentStateChanged, AgentID: agentID,
		PreviousState: string(previous), AgentState: string(codingagent.StateIdle),
	}
}

func waitAgentIdleVoiceDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle voice loop did not stop")
	}
}

type agentIdleVoiceTestStore struct {
	records map[codingagent.ID]codingagent.Record
	err     error
}

func newAgentIdleVoiceTestStore(records ...codingagent.Record) *agentIdleVoiceTestStore {
	store := &agentIdleVoiceTestStore{records: make(map[codingagent.ID]codingagent.Record)}
	for _, record := range records {
		store.records[record.ID] = record
	}
	return store
}

func (s *agentIdleVoiceTestStore) Get(id codingagent.ID) (codingagent.Record, error) {
	if s.err != nil {
		return codingagent.Record{}, s.err
	}
	record, ok := s.records[id]
	if !ok {
		return codingagent.Record{}, fmt.Errorf("%w: %q", codingagent.ErrNotFound, id)
	}
	return record, nil
}

type agentIdleVoiceTestQueue struct {
	mu            sync.Mutex
	errors        []error
	notifications []voice.Notification
}

func (q *agentIdleVoiceTestQueue) Enqueue(notification voice.Notification) (voice.EnqueueStatus, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.notifications = append(q.notifications, notification)
	var err error
	if len(q.errors) > 0 {
		err = q.errors[0]
		q.errors = q.errors[1:]
	}
	if err != nil {
		return "", err
	}
	return voice.EnqueueStatusQueued, nil
}

func (q *agentIdleVoiceTestQueue) snapshot() []voice.Notification {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]voice.Notification(nil), q.notifications...)
}
