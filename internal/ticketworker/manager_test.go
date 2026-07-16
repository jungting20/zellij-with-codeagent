package ticketworker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestManagerInitialFillPreservesZellijSession(t *testing.T) {
	client := newFakeManagerClient()
	manager := newTestManager(t, client, make(chan time.Time), 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	creates := client.waitForCreates(t, 2)
	if got, want := []string{creates[0].ID, creates[1].ID}, []string{"ticket-worker-slot-1-0001", "ticket-worker-slot-2-0001"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pane IDs = %v, want %v", got, want)
	}
	for _, req := range creates {
		if req.ZellijSession != "physical-a" {
			t.Fatalf("worker zellij session = %q, want physical-a", req.ZellijSession)
		}
		if req.TaskID != "tickets" || req.Role != "ticket-worker" || req.SameTabAsPaneID != "ticket-worker-manager" {
			t.Fatalf("unexpected pane identity request: %+v", req)
		}
		if req.CWD != "/repo" || !reflect.DeepEqual(req.Command, []string{"worker", "--once"}) {
			t.Fatalf("unexpected worker invocation request: %+v", req)
		}
	}
	client.waitForWatches(t, 2)
	client.assertWatchMarker(t, "ticket-worker-slot-1-0001", "DONE")
	client.assertWatchMarker(t, "ticket-worker-slot-2-0001", "DONE")

	client.assertCreateCount(t, 2)
}

func TestManagerWaitsForRegisteredAnchorBeforeInitialFill(t *testing.T) {
	client := newFakeManagerClient()
	client.setDefaultInspection(transport.InspectRuntimeResponse{})
	manager := newTestManager(t, client, make(chan time.Time), 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForInspections(t, 1)
	client.assertCreateCount(t, 0)
	client.setDefaultInspection(validAnchorInspection())
	client.waitForCreates(t, 2)
}

func TestManagerRejectsWrongTaskOrSessionAsAnchor(t *testing.T) {
	client := newFakeManagerClient()
	client.queueInspections(
		fakeInspectionResult{response: transport.InspectRuntimeResponse{Panes: []transport.Pane{{ID: "ticket-worker-manager", TaskID: "wrong-task", SessionID: "physical-a", Status: "running"}}}},
		fakeInspectionResult{response: transport.InspectRuntimeResponse{Panes: []transport.Pane{{ID: "ticket-worker-manager", TaskID: "tickets", SessionID: "wrong-session", Status: "running"}}}},
		fakeInspectionResult{response: validAnchorInspection()},
	)
	manager := newTestManager(t, client, make(chan time.Time), 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForCreates(t, 1)
	client.assertInspectionCount(t, 3)
}

func TestManagerAnchorReadinessTimeoutCreatesNoWorkers(t *testing.T) {
	client := newFakeManagerClient()
	client.setDefaultInspection(transport.InspectRuntimeResponse{})
	manager := newTestManager(t, client, make(chan time.Time), 1)
	manager.startupTimeout = 75 * time.Millisecond
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(context.Background()) }()

	select {
	case err := <-runDone:
		for _, want := range []string{"anchor not ready", "ticket-worker-manager", "tickets", "physical-a"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("Run error = %v, want %q", err, want)
			}
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run error = %v, want anchor-not-ready deadline error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after startup timeout")
	}
	client.assertCreateCount(t, 0)
}

func TestManagerRejectsAnchorReturnedAfterReadinessDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &lateAnchorManagerClient{
		fakeManagerClient: newFakeManagerClient(),
		cancelRun:         cancel,
	}
	manager := newTestManager(t, client, make(chan time.Time), 1)
	manager.startupTimeout = time.Millisecond

	err := manager.Run(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want readiness deadline error", err)
	}
	client.assertCreateCount(t, 0)
}

func TestManagerAnchorReadinessCancellationCreatesNoWorkers(t *testing.T) {
	client := newFakeManagerClient()
	client.setDefaultInspection(transport.InspectRuntimeResponse{})
	manager := newTestManager(t, client, make(chan time.Time), 1)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(ctx) }()

	client.waitForInspections(t, 1)
	cancel()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "anchor not ready") {
			t.Fatalf("Run error = %v, want anchor-not-ready cancellation error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	client.assertCreateCount(t, 0)
}

func TestManagerCompletesAndRefillsOnNextTick(t *testing.T) {
	client := newFakeManagerClient()
	ticks := make(chan time.Time, 1)
	manager := newTestManager(t, client, ticks, 2)
	events := newManagerEventBarrier(manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForCreates(t, 2)
	client.match("ticket-worker-slot-1-0001")
	client.waitForCloses(t, 1)
	events.wait(t)
	client.assertCreateCount(t, 2)

	ticks <- time.Unix(101, 0)
	creates := client.waitForCreates(t, 3)
	if got, want := creates[2].ID, "ticket-worker-slot-1-0002"; got != want {
		t.Fatalf("replacement pane ID = %q, want %q", got, want)
	}
}

func TestManagerRetriesCreateFailureOnTickWithNewID(t *testing.T) {
	client := newFakeManagerClient()
	client.failNextCreates(1)
	ticks := make(chan time.Time, 1)
	manager := newTestManager(t, client, ticks, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	creates := client.waitForCreates(t, 2)
	if got, want := []string{creates[0].ID, creates[1].ID}, []string{"ticket-worker-slot-1-0001", "ticket-worker-slot-2-0001"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initial pane IDs = %v, want %v", got, want)
	}
	client.assertCreateCount(t, 2)

	ticks <- time.Unix(101, 0)
	creates = client.waitForCreates(t, 3)
	if got, want := creates[2].ID, "ticket-worker-slot-1-0002"; got != want {
		t.Fatalf("retry pane ID = %q, want %q", got, want)
	}
}

func TestManagerCloseFailurePreservesCapacity(t *testing.T) {
	client := newFakeManagerClient()
	ticks := make(chan time.Time, 1)
	manager := newTestManager(t, client, ticks, 1)
	events := newManagerEventBarrier(manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForCreates(t, 1)
	client.setDefaultInspection(workerInspection("ticket-worker-slot-1-0001"))
	client.failClose("ticket-worker-slot-1-0001")
	client.match("ticket-worker-slot-1-0001")
	client.waitForCloses(t, 1)
	events.wait(t)
	ticks <- time.Unix(101, 0)
	events.wait(t)
	client.assertCreateCount(t, 1)
}

func TestManagerCloseNotFoundWithAbsentWorkerReleasesSlot(t *testing.T) {
	client := newFakeManagerClient()
	ticks := make(chan time.Time, 1)
	manager := newTestManager(t, client, ticks, 1)
	events := newManagerEventBarrier(manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForCreates(t, 1)
	client.failCloseWith("ticket-worker-slot-1-0001", &transport.ClientError{APIError: transport.APIError{Code: transport.CodeNotFound, Message: "pane disappeared"}})
	client.match("ticket-worker-slot-1-0001")
	client.waitForCloses(t, 1)
	events.wait(t)
	ticks <- time.Unix(101, 0)
	client.waitForCreates(t, 2)
}

func TestManagerCloseRuntimeErrorWithAbsentWorkerReleasesSlot(t *testing.T) {
	client := newFakeManagerClient()
	ticks := make(chan time.Time, 1)
	manager := newTestManager(t, client, ticks, 1)
	events := newManagerEventBarrier(manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForCreates(t, 1)
	client.failCloseWith("ticket-worker-slot-1-0001", &transport.ClientError{APIError: transport.APIError{Code: transport.CodeRuntimeError, Message: "zellij close raced"}})
	client.match("ticket-worker-slot-1-0001")
	client.waitForCloses(t, 1)
	events.wait(t)
	ticks <- time.Unix(101, 0)
	client.waitForCreates(t, 2)
}

func TestManagerCloseFailureWithPresentWorkerPreservesCapacity(t *testing.T) {
	client := newFakeManagerClient()
	ticks := make(chan time.Time, 1)
	manager := newTestManager(t, client, ticks, 1)
	events := newManagerEventBarrier(manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForCreates(t, 1)
	client.setDefaultInspection(workerInspection("ticket-worker-slot-1-0001"))
	client.failClose("ticket-worker-slot-1-0001")
	client.match("ticket-worker-slot-1-0001")
	client.waitForCloses(t, 1)
	events.wait(t)
	ticks <- time.Unix(101, 0)
	events.wait(t)
	client.assertCreateCount(t, 1)
}

func TestManagerCloseFailureWithInspectionFailurePreservesCapacity(t *testing.T) {
	client := newFakeManagerClient()
	ticks := make(chan time.Time, 1)
	manager := newTestManager(t, client, ticks, 1)
	events := newManagerEventBarrier(manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForCreates(t, 1)
	client.setDefaultInspectionResult(fakeInspectionResult{err: errors.New("inspect failed")})
	client.failClose("ticket-worker-slot-1-0001")
	client.match("ticket-worker-slot-1-0001")
	client.waitForCloses(t, 1)
	events.wait(t)
	ticks <- time.Unix(101, 0)
	events.wait(t)
	client.assertCreateCount(t, 1)
}

func TestManagerWatchFailurePreservesCapacity(t *testing.T) {
	client := newFakeManagerClient()
	ticks := make(chan time.Time, 1)
	manager := newTestManager(t, client, ticks, 1)
	events := newManagerEventBarrier(manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)

	client.waitForCreates(t, 1)
	client.failWatch("ticket-worker-slot-1-0001")
	client.waitForWatchReturns(t, 1)
	events.wait(t)
	ticks <- time.Unix(101, 0)
	events.wait(t)
	client.assertCreateCount(t, 1)
	client.assertCloseCount(t, 0)
}

func TestManagerCancellationDoesNotCloseWorkers(t *testing.T) {
	client := newFakeManagerClient()
	manager := newTestManager(t, client, make(chan time.Time), 2)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(ctx) }()

	client.waitForCreates(t, 2)
	client.waitForWatches(t, 2)
	cancel()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	client.waitForWatchReturns(t, 2)
	client.assertCloseCount(t, 0)
}

func TestManagerCanceledContextDoesNotCloseReadyCompletion(t *testing.T) {
	client := newFakeManagerClient()
	manager := newTestManager(t, client, make(chan time.Time), 1)
	manager.slots[0].state = slotOccupied
	manager.slots[0].paneID = "ticket-worker-slot-1-0001"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manager.handleWatchResult(ctx, watchResult{
		slotNumber: 1,
		paneID:     "ticket-worker-slot-1-0001",
		response:   transport.WaitForOutputMarkerResponse{PaneID: "ticket-worker-slot-1-0001", Marker: "DONE"},
	})

	client.assertCloseCount(t, 0)
}

func TestManagerCancellationBeforeCloseDispatchDoesNotClose(t *testing.T) {
	client := newFakeManagerClient()
	manager := newTestManager(t, client, make(chan time.Time), 1)
	manager.slots[0].state = slotOccupied
	manager.slots[0].paneID = "ticket-worker-slot-1-0001"
	beforeClose := make(chan struct{})
	releaseClose := make(chan struct{})
	manager.beforeClose = func() {
		close(beforeClose)
		<-releaseClose
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.handleWatchResult(ctx, watchResult{
			slotNumber: 1,
			paneID:     "ticket-worker-slot-1-0001",
			response:   transport.WaitForOutputMarkerResponse{PaneID: "ticket-worker-slot-1-0001", Marker: "DONE"},
		})
		close(done)
	}()

	<-beforeClose
	cancel()
	close(releaseClose)
	<-done
	client.assertCloseCount(t, 0)
}

func TestManagerRejectsMismatchedWatchResponse(t *testing.T) {
	tests := []struct {
		name     string
		response transport.WaitForOutputMarkerResponse
	}{
		{name: "other pane", response: transport.WaitForOutputMarkerResponse{PaneID: "other-pane", Marker: "DONE"}},
		{name: "other marker", response: transport.WaitForOutputMarkerResponse{PaneID: "ticket-worker-slot-1-0001", Marker: "NOT DONE"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newFakeManagerClient()
			manager := newTestManager(t, client, make(chan time.Time), 1)
			manager.slots[0].state = slotOccupied
			manager.slots[0].paneID = "ticket-worker-slot-1-0001"

			manager.handleWatchResult(context.Background(), watchResult{
				slotNumber: 1,
				paneID:     "ticket-worker-slot-1-0001",
				response:   test.response,
			})

			client.assertCloseCount(t, 0)
		})
	}
}

func newTestManager(t *testing.T, client ManagerClient, ticks <-chan time.Time, maxWorkers int) *Manager {
	t.Helper()
	manager, err := NewManager(ManagerOptions{
		Client:        client,
		Config:        Config{MaxWorkers: maxWorkers, PollInterval: time.Second, Worker: WorkerConfig{Command: []string{"worker", "--once"}, CompletionMarker: "DONE"}},
		TaskID:        "tickets",
		AnchorPaneID:  "ticket-worker-manager",
		CWD:           "/repo",
		ZellijSession: " physical-a ",
		Tick:          ticks,
		Now:           func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func validAnchorInspection() transport.InspectRuntimeResponse {
	return transport.InspectRuntimeResponse{Panes: []transport.Pane{{
		ID:        "ticket-worker-manager",
		TaskID:    "tickets",
		SessionID: "physical-a",
		Status:    "running",
	}}}
}

func workerInspection(paneID string) transport.InspectRuntimeResponse {
	response := validAnchorInspection()
	response.Panes = append(response.Panes, transport.Pane{
		ID:        paneID,
		TaskID:    "tickets",
		SessionID: "physical-a",
		Status:    "running",
	})
	return response
}

func TestNewManagerRejectsMissingZellijSession(t *testing.T) {
	_, err := NewManager(ManagerOptions{
		Client:       newFakeManagerClient(),
		Config:       Config{MaxWorkers: 1, PollInterval: time.Second, Worker: WorkerConfig{Command: []string{"worker"}, CompletionMarker: "DONE"}},
		TaskID:       "tickets",
		AnchorPaneID: "ticket-worker-manager",
	})
	if err == nil || !strings.Contains(err.Error(), "zellij session is required") {
		t.Fatalf("NewManager() error = %v, want missing zellij session error", err)
	}
}

func TestNewManagerRejectsNegativeStartupTimeout(t *testing.T) {
	_, err := NewManager(ManagerOptions{
		Client:         newFakeManagerClient(),
		Config:         Config{MaxWorkers: 1, PollInterval: time.Second, Worker: WorkerConfig{Command: []string{"worker"}, CompletionMarker: "DONE"}},
		TaskID:         "tickets",
		AnchorPaneID:   "ticket-worker-manager",
		ZellijSession:  "physical-a",
		StartupTimeout: -time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "startup timeout must not be negative") {
		t.Fatalf("NewManager() error = %v, want negative startup timeout error", err)
	}
}

type managerEventBarrier chan struct{}

func newManagerEventBarrier(manager *Manager) managerEventBarrier {
	barrier := make(managerEventBarrier, 2)
	manager.afterEvent = func() { barrier <- struct{}{} }
	return barrier
}

func (b managerEventBarrier) wait(t *testing.T) {
	t.Helper()
	select {
	case <-b:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for manager event loop")
	}
}

type fakeWatchResult struct {
	response transport.WaitForOutputMarkerResponse
	err      error
}

type fakeInspectionResult struct {
	response transport.InspectRuntimeResponse
	err      error
}

type fakeManagerClient struct {
	mu                sync.Mutex
	changed           chan struct{}
	creates           []transport.CreatePaneRequest
	closes            []string
	watches           map[string]chan fakeWatchResult
	watchRequests     map[string][]transport.WaitForOutputMarkerRequest
	createFailures    int
	closeFailures     map[string]error
	watchStartCount   int
	watchReturnCount  int
	inspectionQueue   []fakeInspectionResult
	inspectionCalls   int
	inspectionDefault fakeInspectionResult
}

type lateAnchorManagerClient struct {
	*fakeManagerClient
	cancelRun context.CancelFunc
}

func (c *lateAnchorManagerClient) InspectRuntime(ctx context.Context) (transport.InspectRuntimeResponse, error) {
	<-ctx.Done()
	return validAnchorInspection(), nil
}

func (c *lateAnchorManagerClient) CreatePane(ctx context.Context, req transport.CreatePaneRequest) (transport.CreatePaneResponse, error) {
	response, err := c.fakeManagerClient.CreatePane(ctx, req)
	c.cancelRun()
	return response, err
}

func newFakeManagerClient() *fakeManagerClient {
	return &fakeManagerClient{
		changed:           make(chan struct{}, 1),
		watches:           make(map[string]chan fakeWatchResult),
		watchRequests:     make(map[string][]transport.WaitForOutputMarkerRequest),
		closeFailures:     make(map[string]error),
		inspectionDefault: fakeInspectionResult{response: validAnchorInspection()},
	}
}

func (f *fakeManagerClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectionCalls++
	result := f.inspectionDefault
	if len(f.inspectionQueue) > 0 {
		result = f.inspectionQueue[0]
		f.inspectionQueue = f.inspectionQueue[1:]
	}
	f.signalLocked()
	return result.response, result.err
}

func (f *fakeManagerClient) queueInspections(results ...fakeInspectionResult) {
	f.mu.Lock()
	f.inspectionQueue = append(f.inspectionQueue, results...)
	f.mu.Unlock()
}

func (f *fakeManagerClient) setDefaultInspection(response transport.InspectRuntimeResponse) {
	f.setDefaultInspectionResult(fakeInspectionResult{response: response})
}

func (f *fakeManagerClient) setDefaultInspectionResult(result fakeInspectionResult) {
	f.mu.Lock()
	f.inspectionDefault = result
	f.signalLocked()
	f.mu.Unlock()
}

func (f *fakeManagerClient) CreatePane(_ context.Context, req transport.CreatePaneRequest) (transport.CreatePaneResponse, error) {
	f.mu.Lock()
	f.creates = append(f.creates, req)
	if f.createFailures > 0 {
		f.createFailures--
		f.signalLocked()
		f.mu.Unlock()
		return transport.CreatePaneResponse{}, errors.New("create failed")
	}
	f.watchChannelLocked(req.ID)
	f.signalLocked()
	f.mu.Unlock()
	return transport.CreatePaneResponse{Pane: transport.Pane{ID: req.ID}}, nil
}

func (f *fakeManagerClient) WaitForOutputMarker(ctx context.Context, paneID string, req transport.WaitForOutputMarkerRequest) (transport.WaitForOutputMarkerResponse, error) {
	f.mu.Lock()
	watch := f.watchChannelLocked(paneID)
	f.watchRequests[paneID] = append(f.watchRequests[paneID], req)
	f.watchStartCount++
	f.signalLocked()
	f.mu.Unlock()

	select {
	case result := <-watch:
		f.recordWatchReturn()
		return result.response, result.err
	case <-ctx.Done():
		f.recordWatchReturn()
		return transport.WaitForOutputMarkerResponse{}, ctx.Err()
	}
}

func (f *fakeManagerClient) ClosePane(_ context.Context, paneID string) (transport.ClosePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes = append(f.closes, paneID)
	f.signalLocked()
	if err := f.closeFailures[paneID]; err != nil {
		return transport.ClosePaneResponse{}, err
	}
	return transport.ClosePaneResponse{Pane: transport.Pane{ID: paneID}}, nil
}

func (f *fakeManagerClient) failNextCreates(count int) {
	f.mu.Lock()
	f.createFailures = count
	f.mu.Unlock()
}

func (f *fakeManagerClient) failClose(paneID string) {
	f.failCloseWith(paneID, errors.New("close failed"))
}

func (f *fakeManagerClient) failCloseWith(paneID string, err error) {
	f.mu.Lock()
	f.closeFailures[paneID] = err
	f.mu.Unlock()
}

func (f *fakeManagerClient) match(paneID string) {
	f.sendWatch(paneID, fakeWatchResult{response: transport.WaitForOutputMarkerResponse{PaneID: paneID, Marker: "DONE", MatchedAt: time.Unix(100, 0)}})
}

func (f *fakeManagerClient) failWatch(paneID string) {
	f.sendWatch(paneID, fakeWatchResult{err: errors.New("watch failed")})
}

func (f *fakeManagerClient) sendWatch(paneID string, result fakeWatchResult) {
	f.mu.Lock()
	watch := f.watchChannelLocked(paneID)
	f.mu.Unlock()
	watch <- result
}

func (f *fakeManagerClient) waitForCreates(t *testing.T, count int) []transport.CreatePaneRequest {
	t.Helper()
	f.waitFor(t, func() bool { return len(f.creates) >= count }, "create calls")
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]transport.CreatePaneRequest(nil), f.creates...)
}

func (f *fakeManagerClient) waitForCloses(t *testing.T, count int) {
	t.Helper()
	f.waitFor(t, func() bool { return len(f.closes) >= count }, "close calls")
}

func (f *fakeManagerClient) waitForWatchReturns(t *testing.T, count int) {
	t.Helper()
	f.waitFor(t, func() bool { return f.watchReturnCount >= count }, "watch returns")
}

func (f *fakeManagerClient) waitForWatches(t *testing.T, count int) {
	t.Helper()
	f.waitFor(t, func() bool { return f.watchStartCount >= count }, "watch calls")
}

func (f *fakeManagerClient) waitForInspections(t *testing.T, count int) {
	t.Helper()
	f.waitFor(t, func() bool { return f.inspectionCalls >= count }, "runtime inspections")
}

func (f *fakeManagerClient) assertInspectionCount(t *testing.T, want int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := f.inspectionCalls; got != want {
		t.Fatalf("inspection count = %d, want %d", got, want)
	}
}

func (f *fakeManagerClient) assertCreateCount(t *testing.T, want int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := len(f.creates); got != want {
		t.Fatalf("create count = %d, want %d", got, want)
	}
}

func (f *fakeManagerClient) assertCloseCount(t *testing.T, want int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := len(f.closes); got != want {
		t.Fatalf("close count = %d, want %d", got, want)
	}
}

func (f *fakeManagerClient) assertWatchMarker(t *testing.T, paneID, want string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	requests := f.watchRequests[paneID]
	if len(requests) != 1 || requests[0].Marker != want {
		t.Fatalf("watch requests for %s = %+v, want one request with marker %q", paneID, requests, want)
	}
}

func (f *fakeManagerClient) waitFor(t *testing.T, ready func() bool, description string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		ok := ready()
		f.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-f.changed:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		}
	}
}

func (f *fakeManagerClient) watchChannelLocked(paneID string) chan fakeWatchResult {
	watch := f.watches[paneID]
	if watch == nil {
		watch = make(chan fakeWatchResult, 1)
		f.watches[paneID] = watch
	}
	return watch
}

func (f *fakeManagerClient) signalLocked() {
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

func (f *fakeManagerClient) recordWatchReturn() {
	f.mu.Lock()
	f.watchReturnCount++
	f.signalLocked()
	f.mu.Unlock()
}
