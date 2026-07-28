package runtime

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func TestCreatePaneWaitsForReadyTextBeforeInitialInput(t *testing.T) {
	backend := &fakeBackend{
		createID:    "terminal_ready",
		dumpOutputs: []string{"starting", "OpenAI Codex\n›"},
	}
	service := newTestService(backend)

	response, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a",
		InitialInput: "implement ticket\n", InitialInputReadyText: "›",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if response.Pane.ID != "coder" || len(backend.dumpRequests) != 2 {
		t.Fatalf("response/dumps = %#v/%d", response.Pane, len(backend.dumpRequests))
	}
	want := []zellij.SendInputRequest{{
		Session: "physical-a", PaneID: "terminal_ready", Text: "implement ticket\n",
	}}
	if !reflect.DeepEqual(backend.sendRequests, want) {
		t.Fatalf("SendInput requests = %#v, want %#v", backend.sendRequests, want)
	}
}

func TestCreatePaneSendsInitialInputImmediatelyWithoutReadyText(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_immediate"}
	service := newTestService(backend)
	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "go\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.dumpRequests) != 0 || len(backend.sendRequests) != 1 {
		t.Fatalf("dump/send = %d/%d, want 0/1", len(backend.dumpRequests), len(backend.sendRequests))
	}
}

func TestCreatePaneIgnoresReadyTextWithoutInitialInput(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_empty"}
	service := newTestService(backend)
	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInputReadyText: "›",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.dumpRequests) != 0 || len(backend.sendRequests) != 0 {
		t.Fatalf("dump/send = %d/%d, want 0/0", len(backend.dumpRequests), len(backend.sendRequests))
	}
}

func TestCreatePaneSharesInitialInputResultForConcurrentIdenticalRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	backend := &fakeBackend{createID: "terminal_ready", dumpOutput: "›"}
	var firstDump sync.Once
	backend.beforeDumpScreen = func(_ context.Context, _ zellij.DumpScreenRequest) error {
		firstDump.Do(func() {
			close(started)
			<-release
		})
		return nil
	}
	service := newTestService(backend)
	req := CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a",
		InitialInput: "implement ticket\n", InitialInputReadyText: "›",
	}

	type result struct {
		response CreatePaneResponse
		err      error
	}
	results := make(chan result, 2)
	go func() {
		response, err := service.CreatePane(context.Background(), req)
		results <- result{response: response, err: err}
	}()
	<-started
	go func() {
		response, err := service.CreatePane(context.Background(), req)
		results <- result{response: response, err: err}
	}()
	close(release)

	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("CreatePane errors = %v, %v", first.err, second.err)
	}
	if first.response.Pane.ID != "coder" || second.response.Pane.ID != "coder" {
		t.Fatalf("CreatePane responses = %#v, %#v", first.response, second.response)
	}
	if len(backend.createRequests) != 1 || len(backend.sendRequests) != 1 {
		t.Fatalf("create/send = %d/%d, want 1/1", len(backend.createRequests), len(backend.sendRequests))
	}
}

func TestCreatePaneInitialInputFailureRollsBack(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_failed", sendErr: errors.New("paste failed")}
	service := newTestService(backend)

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "go\n",
	})
	if !errors.Is(err, ErrPaneInitializationFailed) {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if len(backend.closeRequests) != 1 || backend.closeRequests[0].PaneID != "terminal_failed" {
		t.Fatalf("ClosePane requests = %#v", backend.closeRequests)
	}
	if _, getErr := service.registry.GetPane("coder"); !errors.Is(getErr, registry.ErrNotFound) {
		t.Fatalf("GetPane() error = %v, want not found", getErr)
	}
}

func TestCreatePaneInitializationCleanupFailurePreservesRegistry(t *testing.T) {
	backend := &fakeBackend{
		createID: "terminal_leaked",
		sendErr:  errors.New("paste failed"),
		closeErr: errors.New("close failed"),
	}
	service := newTestService(backend)

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "go\n",
	})
	if !errors.Is(err, ErrPaneInitializationFailed) || !errors.Is(err, ErrCleanupPartial) {
		t.Fatalf("CreatePane() error = %v", err)
	}
	record, getErr := service.registry.GetPane("coder")
	if getErr != nil || record.ZellijPaneID != "terminal_leaked" {
		t.Fatalf("record/error = %#v/%v", record, getErr)
	}
}

func TestCreatePaneReadinessTimeoutRollsBackWithFreshContext(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_timeout", dumpOutput: "starting"}
	service := newTestService(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := service.CreatePane(ctx, CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "go\n", InitialInputReadyText: "›",
	})
	if !errors.Is(err, ErrPaneInitializationFailed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if len(backend.closeRequests) != 1 {
		t.Fatalf("ClosePane requests = %#v, want one timeout rollback", backend.closeRequests)
	}
	if len(backend.closeContextErrs) != 1 || backend.closeContextErrs[0] != nil {
		t.Fatalf("ClosePane context errors = %#v, want fresh rollback context", backend.closeContextErrs)
	}
	if _, getErr := service.registry.GetPane("coder"); !errors.Is(getErr, registry.ErrNotFound) {
		t.Fatalf("GetPane() error = %v, want not found", getErr)
	}
}

func TestCreatePaneInitializationRollbackStopsSubscription(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_subscribed", dumpOutput: "starting"}
	started := make(chan struct{})
	runner := &scriptedSubscriptionRunner{fn: func(ctx context.Context, _ zellij.CommandSpec, _ *io.PipeWriter) {
		close(started)
		<-ctx.Done()
	}}
	service := NewService(Options{
		Registry:           registry.New(),
		Backend:            backend,
		SubscriptionRunner: runner,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := service.CreatePane(ctx, CreatePaneRequest{
			ID: "coder", ZellijSession: "physical-a", InitialInput: "go\n", InitialInputReadyText: "›",
		})
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pane subscription")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrPaneInitializationFailed) || !errors.Is(err, context.Canceled) {
			t.Fatalf("CreatePane() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initialization rollback")
	}

	service.subs.mu.Lock()
	_, subscribed := service.subs.cancelByPaneID["coder"]
	service.subs.mu.Unlock()
	if subscribed {
		t.Fatal("subscription entry remains after initialization rollback")
	}
	if _, getErr := service.registry.GetPane("coder"); !errors.Is(getErr, registry.ErrNotFound) {
		t.Fatalf("GetPane() error = %v, want not found", getErr)
	}
}

func TestCreatePaneInitialInputDoesNotReachReusedPane(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_old", dumpOutput: "›"}
	service := newTestService(backend)
	backend.beforeDumpScreen = func(context.Context, zellij.DumpScreenRequest) error {
		if _, err := service.registry.RemovePane("coder"); err != nil {
			return err
		}
		_, err := service.registry.RegisterPane(registry.RegisterPaneRequest{
			ID:           "coder",
			TaskID:       "replacement-task",
			ZellijPaneID: "terminal_replacement",
		})
		return err
	}

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "old input\n", InitialInputReadyText: "›",
	})
	if !errors.Is(err, ErrPaneInitializationFailed) || !errors.Is(err, registry.ErrStaleRecord) {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if len(backend.sendRequests) != 0 {
		t.Fatalf("SendInput requests = %#v, want no delivery to replacement pane", backend.sendRequests)
	}
	current, getErr := service.registry.GetPane("coder")
	if getErr != nil || current.ZellijPaneID != "terminal_replacement" {
		t.Fatalf("GetPane() = %#v, %v; want replacement pane", current, getErr)
	}
}

func TestCreatePaneRejectsDifferentInitialInputForReservedLogicalID(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_reserved"}
	service := newTestService(backend)
	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "first input\n",
	}); err != nil {
		t.Fatalf("CreatePane(first) error = %v", err)
	}

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "replacement input\n",
	})
	if !errors.Is(err, registry.ErrAlreadyExists) {
		t.Fatalf("CreatePane() with different initial input error = %v, want %v", err, registry.ErrAlreadyExists)
	}
}
