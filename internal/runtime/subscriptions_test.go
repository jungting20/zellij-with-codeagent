package runtime

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

type scriptedSubscriptionRunner struct {
	fn func(ctx context.Context, spec zellij.CommandSpec, pw *io.PipeWriter)
}

func (r *scriptedSubscriptionRunner) Start(ctx context.Context, spec zellij.CommandSpec) (*SubscriptionStream, error) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		if r.fn != nil {
			r.fn(ctx, spec, pw)
		}
	}()
	return &SubscriptionStream{
		Stdout: pr,
		Wait: func() error {
			return nil
		},
	}, nil
}

type blockingStartSubscriptionRunner struct {
	started chan struct{}
}

func (r *blockingStartSubscriptionRunner) Start(ctx context.Context, _ zellij.CommandSpec) (*SubscriptionStream, error) {
	close(r.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

type recordingPaneObserver struct {
	mu            sync.Mutex
	outputs       []string
	records       []registry.PaneRecord
	closed        []registry.PaneID
	closedRecords []registry.PaneRecord
	errors        []error
}

func (o *recordingPaneObserver) PaneOutput(record registry.PaneRecord, renderedText string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, record)
	o.outputs = append(o.outputs, renderedText)
}

func (o *recordingPaneObserver) PaneClosed(record registry.PaneRecord) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = append(o.closed, record.ID)
	o.closedRecords = append(o.closedRecords, record)
}

func (o *recordingPaneObserver) PaneError(_ registry.PaneRecord, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.errors = append(o.errors, err)
}

func TestSubscriptionManagerObserverReceivesUpdatedOutput(t *testing.T) {
	reg := registry.New()
	record, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: "pane-1", Role: "coding-agent"})
	if err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}
	observer := &recordingPaneObserver{}
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Bus:      eventbus.New(),
		Observer: observer,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription := &paneSubscription{
		cancel: cancel,
		ctx:    ctx,
		done:   make(chan struct{}),
		key:    subscriptionKey{paneID: record.ID, generation: record.Generation},
	}
	mgr.cancelByPaneID[record.ID] = subscription

	mgr.handleLine(record, subscription, `{"name":"pane_update","viewport":[" rendered output "]}`)

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.outputs) != 1 || observer.outputs[0] != "rendered output" {
		t.Fatalf("observer outputs = %#v, want [rendered output]", observer.outputs)
	}
	if got := observer.records[0].LastOutput; got != "rendered output" {
		t.Fatalf("observed record LastOutput = %q, want registry-updated output", got)
	}
}

func TestSubscriptionManagerObserverReceivesRemovedPaneOnce(t *testing.T) {
	reg := registry.New()
	record, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: "pane-1", Role: "coding-agent"})
	if err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}
	observer := &recordingPaneObserver{}
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Bus:      eventbus.New(),
		Observer: observer,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription := &paneSubscription{
		cancel: cancel,
		ctx:    ctx,
		done:   make(chan struct{}),
		key:    subscriptionKey{paneID: record.ID, generation: record.Generation},
	}
	mgr.cancelByPaneID[record.ID] = subscription

	mgr.handleLine(record, subscription, `{"name":"pane_closed"}`)
	mgr.handleLine(record, subscription, `{"name":"pane_closed"}`)

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.closed) != 1 || observer.closed[0] != record.ID {
		t.Fatalf("observer closed = %#v, want [%q]", observer.closed, record.ID)
	}
}

func TestSubscriptionManagerObserverReceivesFailuresWithoutSuppressingEvents(t *testing.T) {
	tests := []struct {
		name      string
		publish   func(*SubscriptionManager, registry.PaneRecord, error)
		wantTypes []eventbus.EventType
	}{
		{
			name: "startup",
			publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
				m.publishSubscribeStartupFailure(record, err)
			},
			wantTypes: []eventbus.EventType{eventbus.TypeSubscribeError, eventbus.TypeHealthChanged},
		},
		{
			name: "parse",
			publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
				m.publishSubscribeParseError(record, err)
			},
			wantTypes: []eventbus.EventType{eventbus.TypeSubscribeError},
		},
		{
			name: "stream",
			publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
				m.publishStreamError(record, err)
			},
			wantTypes: []eventbus.EventType{eventbus.TypeSubscribeError},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := registry.New()
			record, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: "pane-1", Role: "coding-agent"})
			if err != nil {
				t.Fatalf("RegisterPane() error = %v", err)
			}
			bus := eventbus.New()
			observer := &recordingPaneObserver{}
			mgr := NewSubscriptionManager(SubscriptionManagerOptions{Registry: reg, Bus: bus, Observer: observer})
			wantErr := errors.New(tt.name + " failure")

			tt.publish(mgr, record, wantErr)

			observer.mu.Lock()
			if len(observer.errors) != 1 || !errors.Is(observer.errors[0], wantErr) {
				t.Fatalf("observer errors = %#v, want %v", observer.errors, wantErr)
			}
			observer.mu.Unlock()
			events := bus.Recent(0)
			if len(events) != len(tt.wantTypes) {
				t.Fatalf("events = %#v, want types %#v", events, tt.wantTypes)
			}
			for i, want := range tt.wantTypes {
				if events[i].Type != want {
					t.Fatalf("events[%d].Type = %q, want %q", i, events[i].Type, want)
				}
			}
		})
	}
}

func TestSubscriptionManagerObserverErrorHelpersRecheckGenerationAfterPaneReuse(t *testing.T) {
	tests := []struct {
		name    string
		publish func(*SubscriptionManager, registry.PaneRecord, error)
	}{
		{
			name: "startup",
			publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
				m.publishSubscribeStartupFailure(record, err)
			},
		},
		{
			name: "parse",
			publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
				m.publishSubscribeParseError(record, err)
			},
		},
		{
			name: "stream",
			publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
				m.publishStreamError(record, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := registry.New()
			oldRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
				ID: "pane-1", TaskID: "old-task", Role: "coding-agent",
			})
			if err != nil {
				t.Fatalf("RegisterPane(old) error = %v", err)
			}
			observer := &recordingPaneObserver{}
			mgr := NewSubscriptionManager(SubscriptionManagerOptions{
				Registry: reg,
				Bus:      eventbus.New(),
				Observer: observer,
			})
			if _, err := reg.RemovePane(oldRecord.ID); err != nil {
				t.Fatalf("RemovePane(old) error = %v", err)
			}
			newRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
				ID: "pane-1", TaskID: "new-task", Role: "coding-agent",
			})
			if err != nil {
				t.Fatalf("RegisterPane(new) error = %v", err)
			}

			tt.publish(mgr, oldRecord, errors.New("old generation failure"))

			observer.mu.Lock()
			if len(observer.errors) != 0 {
				t.Fatalf("observer errors = %#v, want stale generation ignored", observer.errors)
			}
			observer.mu.Unlock()
			current, err := reg.GetPane(newRecord.ID)
			if err != nil {
				t.Fatalf("GetPane(new) error = %v", err)
			}
			if current.Generation != newRecord.Generation || current.TaskID != "new-task" || current.Status == registry.PaneStatusError {
				t.Fatalf("new generation was changed by stale error: %#v", current)
			}
		})
	}
}

func TestSubscriptionManagerObserverErrorHelpersIgnoreTerminalGeneration(t *testing.T) {
	tests := []struct {
		name    string
		publish func(*SubscriptionManager, registry.PaneRecord, error)
	}{
		{name: "startup", publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
			m.publishSubscribeStartupFailure(record, err)
		}},
		{name: "parse", publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
			m.publishSubscribeParseError(record, err)
		}},
		{name: "stream", publish: func(m *SubscriptionManager, record registry.PaneRecord, err error) {
			m.publishStreamError(record, err)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := registry.New()
			record, err := reg.RegisterPane(registry.RegisterPaneRequest{
				ID: "pane-1", Role: "coding-agent", Status: registry.PaneStatusLost,
			})
			if err != nil {
				t.Fatalf("RegisterPane() error = %v", err)
			}
			observer := &recordingPaneObserver{}
			mgr := NewSubscriptionManager(SubscriptionManagerOptions{Registry: reg, Bus: eventbus.New(), Observer: observer})

			tt.publish(mgr, record, errors.New("late failure"))

			observer.mu.Lock()
			if len(observer.errors) != 0 {
				t.Fatalf("observer errors = %#v, want terminal generation ignored", observer.errors)
			}
			observer.mu.Unlock()
			current, err := reg.GetPane(record.ID)
			if err != nil || current.Status != registry.PaneStatusLost {
				t.Fatalf("terminal pane = %#v, error = %v; want lost preserved", current, err)
			}
		})
	}
}

func TestSubscriptionManagerObserverIgnoresStaleGeneration(t *testing.T) {
	reg := registry.New()
	oldRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: "pane-1", Role: "coding-agent"})
	if err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}
	if _, err := reg.RemovePane(oldRecord.ID); err != nil {
		t.Fatalf("RemovePane(old) error = %v", err)
	}
	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: "pane-1", Role: "coding-agent"}); err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}
	observer := &recordingPaneObserver{}
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{Registry: reg, Bus: eventbus.New(), Observer: observer})

	mgr.handlePaneUpdate(oldRecord, "stale")
	mgr.handlePaneClosed(oldRecord)

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.outputs) != 0 || len(observer.closed) != 0 || len(observer.errors) != 0 {
		t.Fatalf("stale observer calls = outputs:%#v closed:%#v errors:%#v", observer.outputs, observer.closed, observer.errors)
	}
}

func TestSubscriptionManagerObserverIsWiredFromServiceOptions(t *testing.T) {
	observer := &recordingPaneObserver{}
	service := NewService(Options{
		Backend:            &fakeBackend{},
		SubscriptionRunner: &scriptedSubscriptionRunner{},
		PaneObserver:       observer,
	})

	if service.subs == nil || service.subs.opts.Observer != observer {
		t.Fatalf("subscription observer = %#v, want configured observer", service.subs)
	}
}

func TestSubscriptionManagerPublishesRawOutputAndUpdatesRegistry(t *testing.T) {
	reg := registry.New()
	bus := eventbus.New()

	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "pane-1",
		ZellijPaneID: "terminal_5",
		Status:       registry.PaneStatusStarting,
	}); err != nil {
		t.Fatalf("RegisterPane: %v", err)
	}

	runner := &scriptedSubscriptionRunner{
		fn: func(ctx context.Context, spec zellij.CommandSpec, pw *io.PipeWriter) {
			if spec.Name != "zellij" || !strings.Contains(strings.Join(spec.Args, " "), "terminal_5") {
				t.Errorf("unexpected subscribe spec: %#v", spec)
			}
			_, _ = io.WriteString(pw, `{"name":"pane_update","pane_id":"terminal_5","viewport":["hello"]}`+"\n")
		},
	}

	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      bus,
		Runner:   runner,
		Now: func() time.Time {
			return time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := bus.Subscribe(ctx)
	mgr.StartPane("pane-1")

	var sawRaw bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.After(2 * time.Second)
		for !sawRaw {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if ev.Type == eventbus.TypeRawOutput && strings.Contains(ev.Message, "hello") {
					sawRaw = true
					return
				}
			case <-timer:
				return
			}
		}
	}()
	wg.Wait()

	if !sawRaw {
		t.Fatal("expected raw_output event with hello")
	}

	record, err := reg.GetPane("pane-1")
	if err != nil {
		t.Fatalf("GetPane: %v", err)
	}
	if record.LastOutput != "hello" {
		t.Fatalf("LastOutput = %q, want hello", record.LastOutput)
	}
	if record.Status != registry.PaneStatusRunning {
		t.Fatalf("Status = %q, want running", record.Status)
	}
}

func TestSubscriptionManagerRoutesSubscriptionByRecordSession(t *testing.T) {
	reg := registry.New()
	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID: "pane-1", SessionID: "session-a", ZellijPaneID: "terminal_5",
	}); err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}
	backend := &fakeBackend{}
	runner := &scriptedSubscriptionRunner{}
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  backend,
		Bus:      eventbus.New(),
		Runner:   runner,
	})

	mgr.StartPane("pane-1")
	deadline := time.Now().Add(time.Second)
	for {
		backend.mu.Lock()
		requests := append([]zellij.SubscribeRequest(nil), backend.subscribeRequests...)
		backend.mu.Unlock()
		if len(requests) > 0 {
			if got := requests[0].Session; got != "session-a" {
				t.Fatalf("subscribe session = %q, want session-a", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for subscribe request")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSubscriptionManagerDedupesIdenticalViewport(t *testing.T) {
	reg := registry.New()
	bus := eventbus.New()

	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "pane-1",
		ZellijPaneID: "terminal_5",
	}); err != nil {
		t.Fatalf("RegisterPane: %v", err)
	}

	runner := &scriptedSubscriptionRunner{
		fn: func(ctx context.Context, spec zellij.CommandSpec, pw *io.PipeWriter) {
			line := `{"name":"pane_update","pane_id":"terminal_5","viewport":["same"]}` + "\n"
			_, _ = io.WriteString(pw, line)
			_, _ = io.WriteString(pw, line)
		},
	}

	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      bus,
		Runner:   runner,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, _ := bus.Subscribe(ctx)
	mgr.StartPane("pane-1")

	rawCount := 0
	deadline := time.After(400 * time.Millisecond)
	for {
		select {
		case ev := <-out:
			if ev.Type == eventbus.TypeRawOutput {
				rawCount++
			}
		case <-deadline:
			if rawCount != 1 {
				t.Fatalf("raw_output count = %d, want 1 (dedupe)", rawCount)
			}
			return
		}
	}
}

func TestSubscriptionManagerRemovesPaneOnPaneClosed(t *testing.T) {
	reg := registry.New()
	bus := eventbus.New()

	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "pane-1",
		TaskID:       "task-1",
		AgentID:      "agent-1",
		ZellijPaneID: "terminal_5",
	}); err != nil {
		t.Fatalf("RegisterPane: %v", err)
	}

	runner := &scriptedSubscriptionRunner{
		fn: func(ctx context.Context, spec zellij.CommandSpec, pw *io.PipeWriter) {
			_, _ = io.WriteString(pw, `{"name":"pane_closed","pane_id":"terminal_5"}`+"\n")
		},
	}

	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      bus,
		Runner:   runner,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, _ := bus.Subscribe(ctx)
	mgr.StartPane("pane-1")

	var sawClosed bool
	deadline := time.After(2 * time.Second)
	for !sawClosed {
		select {
		case ev := <-out:
			if ev.Type == eventbus.TypePaneClosed {
				if ev.PaneID != "pane-1" || ev.TaskID != "task-1" || ev.AgentID != "agent-1" || ev.ZellijPaneID != "terminal_5" {
					t.Fatalf("pane_closed event = %#v, want removed pane metadata", ev)
				}
				sawClosed = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for pane_closed event")
		}
	}

	if _, err := reg.GetPane("pane-1"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("GetPane() error = %v, want %v", err, registry.ErrNotFound)
	}
}

func TestSubscriptionManagerIgnoresPaneClosedAfterCleanupRemovedRecord(t *testing.T) {
	bus := eventbus.New()
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: registry.New(),
		Bus:      bus,
	})

	mgr.handlePaneClosed(registry.PaneRecord{ID: "coder", Generation: 1})

	if events := bus.Recent(0); len(events) != 0 {
		t.Fatalf("events = %#v, want no subscribe_error for already removed pane", events)
	}
}

func TestSubscriptionManagerStaleGenerationCannotMutateReusedPane(t *testing.T) {
	reg := registry.New()
	oldRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		TaskID:       "old-task",
		ZellijPaneID: "terminal_old",
	})
	if err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}
	if _, err := reg.RemovePane("coder"); err != nil {
		t.Fatalf("RemovePane(old) error = %v", err)
	}
	newRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		TaskID:       "new-task",
		ZellijPaneID: "terminal_new",
	})
	if err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}

	bus := eventbus.New()
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{Registry: reg, Bus: bus})
	mgr.handlePaneUpdate(oldRecord, "stale output")
	mgr.handlePaneClosed(oldRecord)

	current, err := reg.GetPane("coder")
	if err != nil {
		t.Fatalf("GetPane(new) error = %v", err)
	}
	if current.Generation != newRecord.Generation || current.LastOutput != "" || current.TaskID != "new-task" {
		t.Fatalf("current pane = %#v, want untouched new generation", current)
	}
	if events := bus.Recent(0); len(events) != 0 {
		t.Fatalf("events = %#v, want stale generation ignored", events)
	}
}

func TestSubscriptionManagerOldRunDoesNotClearReusedPaneSubscription(t *testing.T) {
	reg := registry.New()
	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		ZellijPaneID: "terminal_old",
	}); err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}

	oldStarted := make(chan struct{})
	newStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	oldWriterDone := make(chan struct{})
	var calls atomic.Int32
	runner := &scriptedSubscriptionRunner{fn: func(ctx context.Context, _ zellij.CommandSpec, pw *io.PipeWriter) {
		switch calls.Add(1) {
		case 1:
			close(oldStarted)
			<-releaseOld
			_, _ = io.WriteString(pw, `{"name":"pane_closed","pane_id":"terminal_old"}`+"\n")
			close(oldWriterDone)
		default:
			close(newStarted)
			<-ctx.Done()
		}
	}}
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      eventbus.New(),
		Runner:   runner,
	})

	mgr.StartPane("coder")
	<-oldStarted
	mgr.StopPane("coder")
	if _, err := reg.RemovePane("coder"); err != nil {
		t.Fatalf("RemovePane(old) error = %v", err)
	}
	newRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		ZellijPaneID: "terminal_new",
	})
	if err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}
	mgr.StartPane("coder")
	<-newStarted

	close(releaseOld)
	<-oldWriterDone
	time.Sleep(20 * time.Millisecond)

	mgr.mu.Lock()
	_, subscribed := mgr.cancelByPaneID["coder"]
	mgr.mu.Unlock()
	if !subscribed {
		t.Fatal("old subscription teardown removed the new subscription")
	}
	current, err := reg.GetPane("coder")
	if err != nil {
		t.Fatalf("GetPane(new) error = %v", err)
	}
	if current.Generation != newRecord.Generation || current.ZellijPaneID != "terminal_new" {
		t.Fatalf("current pane = %#v, want new generation", current)
	}
	mgr.StopPane("coder")
}

func TestSubscriptionManagerObserverCanceledOldStartDoesNotPublishErrorsForReusedPane(t *testing.T) {
	reg := registry.New()
	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		TaskID:       "old-task",
		ZellijPaneID: "terminal_old",
	}); err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}

	bus := eventbus.New()
	observer := &recordingPaneObserver{}
	runner := &blockingStartSubscriptionRunner{started: make(chan struct{})}
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      bus,
		Runner:   runner,
		Observer: observer,
	})

	mgr.StartPane("coder")
	<-runner.started
	mgr.mu.Lock()
	oldSubscription := mgr.cancelByPaneID["coder"]
	mgr.mu.Unlock()
	if oldSubscription == nil {
		t.Fatal("old subscription was not installed")
	}
	mgr.StopPane("coder")
	if _, err := reg.RemovePane("coder"); err != nil {
		t.Fatalf("RemovePane(old) error = %v", err)
	}
	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		TaskID:       "new-task",
		ZellijPaneID: "terminal_new",
	}); err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}

	select {
	case <-oldSubscription.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled old subscription to exit")
	}

	if events := bus.Recent(0); len(events) != 0 {
		t.Fatalf("events = %#v, want canceled old subscription to stay silent", events)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.errors) != 0 {
		t.Fatalf("observer errors = %#v, want canceled old subscription to stay silent", observer.errors)
	}
}

func TestSubscriptionManagerRetriesWhenRecordChangesBeforeSubscriptionInstall(t *testing.T) {
	reg := registry.New()
	oldRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		ZellijPaneID: "terminal_old",
	})
	if err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}
	if _, err := reg.RemovePane("coder"); err != nil {
		t.Fatalf("RemovePane(old) error = %v", err)
	}
	newRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		ZellijPaneID: "terminal_new",
	})
	if err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}

	started := make(chan struct{})
	runner := &scriptedSubscriptionRunner{fn: func(ctx context.Context, _ zellij.CommandSpec, _ *io.PipeWriter) {
		close(started)
		<-ctx.Done()
	}}
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      eventbus.New(),
		Runner:   runner,
	})

	if retry := mgr.startRecord(oldRecord); !retry {
		t.Fatal("startRecord(stale generation) retry = false, want true")
	}
	mgr.StartPane("coder")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for new-generation subscription")
	}

	mgr.mu.Lock()
	subscription := mgr.cancelByPaneID["coder"]
	mgr.mu.Unlock()
	if subscription == nil || subscription.key.generation != newRecord.Generation {
		t.Fatalf("subscription = %#v, want generation %d", subscription, newRecord.Generation)
	}
	mgr.StopPane("coder")
}

func TestSubscriptionManagerOldGenerationStopDoesNotCancelReusedPane(t *testing.T) {
	reg := registry.New()
	oldRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		ZellijPaneID: "terminal_old",
	})
	if err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}
	if _, err := reg.RemovePane("coder"); err != nil {
		t.Fatalf("RemovePane(old) error = %v", err)
	}
	newRecord, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		ZellijPaneID: "terminal_new",
	})
	if err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}

	mgr := NewSubscriptionManager(SubscriptionManagerOptions{Registry: reg})
	ctx, cancel := context.WithCancel(context.Background())
	newSubscription := &paneSubscription{
		cancel: cancel,
		ctx:    ctx,
		done:   make(chan struct{}),
		key: subscriptionKey{
			paneID:     "coder",
			generation: newRecord.Generation,
		},
	}
	mgr.cancelByPaneID["coder"] = newSubscription

	mgr.StopPaneGeneration("coder", oldRecord.Generation)

	mgr.mu.Lock()
	current := mgr.cancelByPaneID["coder"]
	mgr.mu.Unlock()
	if current != newSubscription || ctx.Err() != nil {
		t.Fatalf("subscription = %#v, context error = %v; want new generation still active", current, ctx.Err())
	}
	mgr.StopPane("coder")
}

func TestSubscriptionManagerMissingRecordStartStaysSilent(t *testing.T) {
	bus := eventbus.New()
	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: registry.New(),
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      bus,
		Runner:   &scriptedSubscriptionRunner{},
	})

	mgr.StartPane("coder")

	if events := bus.Recent(0); len(events) != 0 {
		t.Fatalf("events = %#v, want detached missing pane start to stay silent", events)
	}
}

func TestSubscriptionManagerMalformedLineEmitsSubscribeError(t *testing.T) {
	reg := registry.New()
	bus := eventbus.New()

	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "pane-1",
		ZellijPaneID: "terminal_5",
	}); err != nil {
		t.Fatalf("RegisterPane: %v", err)
	}

	runner := &scriptedSubscriptionRunner{
		fn: func(ctx context.Context, spec zellij.CommandSpec, pw *io.PipeWriter) {
			_, _ = io.WriteString(pw, "not-json\n")
		},
	}

	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      bus,
		Runner:   runner,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, _ := bus.Subscribe(ctx)
	mgr.StartPane("pane-1")

	sawErr := false
	for i := 0; i < 50; i++ {
		select {
		case ev := <-out:
			if ev.Type == eventbus.TypeSubscribeError {
				sawErr = true
			}
		case <-time.After(20 * time.Millisecond):
		}
		if sawErr {
			break
		}
	}

	if !sawErr {
		t.Fatal("expected subscribe_error for malformed json")
	}
}

func TestSubscriptionManagerStopPaneClearsMap(t *testing.T) {
	reg := registry.New()
	bus := eventbus.New()

	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           "pane-1",
		ZellijPaneID: "terminal_5",
	}); err != nil {
		t.Fatalf("RegisterPane: %v", err)
	}

	block := make(chan struct{})
	runner := &scriptedSubscriptionRunner{
		fn: func(ctx context.Context, spec zellij.CommandSpec, pw *io.PipeWriter) {
			<-block
			<-ctx.Done()
		},
	}

	mgr := NewSubscriptionManager(SubscriptionManagerOptions{
		Registry: reg,
		Backend:  zellij.NewBackend(zellij.Options{}),
		Bus:      bus,
		Runner:   runner,
	})

	mgr.StartPane("pane-1")
	time.Sleep(30 * time.Millisecond)
	mgr.StopPane("pane-1")
	close(block)
	time.Sleep(30 * time.Millisecond)

	mgr.mu.Lock()
	_, exists := mgr.cancelByPaneID["pane-1"]
	mgr.mu.Unlock()
	if exists {
		t.Fatal("expected pane subscription cleared")
	}
}
