package runtime

import (
	"context"
	"io"
	"strings"
	"sync"
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
