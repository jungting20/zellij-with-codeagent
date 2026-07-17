package ticketworker

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestManagerWaitsForAnchorThenFillsConfiguredCapacity(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(1), managerTicket(2), managerTicket(3)}}
	client := newFakeManagerClient()
	client.anchorReady = false
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	manager := newTestManager(t, store, client, 2)

	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return client.inspections() > 0 })
	if store.nextCount() != 0 || len(client.created()) != 0 || client.streamCalls() != 0 {
		t.Fatalf("before anchor: next=%d creates=%d streams=%d", store.nextCount(), len(client.created()), client.streamCalls())
	}

	client.setAnchorReady(true)
	waitFor(t, func() bool { return len(client.created()) == 2 && len(client.inputs()) == 2 })
	if store.nextCount() != 2 {
		t.Fatalf("Next calls = %d, want 2", store.nextCount())
	}
	for i, req := range client.created() {
		wantID := int64(i + 1)
		if req.ID != "ticket-coding-"+string(rune('0'+wantID)) || req.Role != "coding-agent" || req.TaskID != "tickets" || req.SameTabAsPaneID != "ticket-manager" || req.ZellijSession != "physical-a" {
			t.Fatalf("create[%d] = %#v", i, req)
		}
		wantCommand := []string{"zellij-agent", "role", "coding-agent", "/repo"}
		if len(req.Command) != len(wantCommand) {
			t.Fatalf("command = %#v", req.Command)
		}
		for j := range wantCommand {
			if req.Command[j] != wantCommand[j] {
				t.Fatalf("command = %#v", req.Command)
			}
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() shutdown error = %v", err)
	}
}

func TestManagerRejectsWrongAnchorIdentityAndDoesNotClaim(t *testing.T) {
	for name, identity := range map[string][2]string{
		"wrong task":    {"other-task", "physical-a"},
		"wrong session": {"tickets", "other-session"},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeManagerStore{ready: []Ticket{managerTicket(1)}}
			client := newFakeManagerClient()
			client.anchorTaskID = identity[0]
			client.anchorSessionID = identity[1]
			manager, err := NewManager(ManagerOptions{
				Store: store, Client: client,
				Config: Config{Version: 1, MaxWorkers: 1, PollInterval: time.Hour, PromptTemplate: "Ticket {{ .ID }}"},
				Root:   "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
				StartupTimeout: 10 * time.Millisecond, ReadyPollInterval: time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Run(context.Background()); err == nil {
				t.Fatal("Run() error = nil, want anchor timeout")
			}
			if store.nextCount() != 0 || client.streamCalls() != 0 {
				t.Fatalf("wrong anchor caused work: next=%d streams=%d", store.nextCount(), client.streamCalls())
			}
		})
	}
}

func TestManagerDoesNotClaimWhenInitialStreamFails(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(1)}}
	client := newFakeManagerClient()
	manager := newTestManager(t, store, client, 1)
	if err := manager.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want stream connection failure")
	}
	if store.nextCount() != 0 || len(client.created()) != 0 {
		t.Fatalf("stream failure caused work: next=%d creates=%d", store.nextCount(), len(client.created()))
	}
}

func TestManagerIgnoresPromptEchoAndCompletesExactMarker(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(42)}}
	client := newFakeManagerClient()
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	manager := newTestManager(t, store, client, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.inputs()) == 1 })

	prompt := client.inputs()[0].req.Text
	stream.events <- transport.Event{Type: "raw_output", PaneID: "ticket-coding-42", Message: prompt}
	time.Sleep(20 * time.Millisecond)
	if len(store.transitions()) != 0 || len(client.closed()) != 0 {
		t.Fatalf("prompt echo completed ticket: transitions=%v closes=%v", store.transitions(), client.closed())
	}
	for _, output := range []string{
		"prefix ZELLIJ_AGENT_TICKET_DONE 42",
		"ZELLIJ_AGENT_TICKET_DONE 42 suffix",
		"ZELLIJ_AGENT_TICKET_DONE 43",
	} {
		stream.events <- transport.Event{Type: "raw_output", PaneID: "ticket-coding-42", Message: output}
	}
	time.Sleep(20 * time.Millisecond)
	if len(store.transitions()) != 0 {
		t.Fatalf("non-exact output completed ticket: %v", store.transitions())
	}

	stream.events <- transport.Event{Type: "raw_output", PaneID: "ticket-coding-42", Message: "work\n  ZELLIJ_AGENT_TICKET_DONE 42  \n"}
	waitFor(t, func() bool { return len(store.transitions()) == 1 && len(client.closed()) == 1 })
	if call := store.transitions()[0]; call.id != 42 || call.action != ActionDone {
		t.Fatalf("transition = %#v", call)
	}
	if sequence := append(store.callSequence(), client.callSequence()...); indexOf(sequence, "done:42") < 0 {
		t.Fatalf("call sequence = %#v", sequence)
	}
	stream.events <- transport.Event{Type: "raw_output", PaneID: "ticket-coding-42", Message: "ZELLIJ_AGENT_TICKET_DONE 42"}
	time.Sleep(20 * time.Millisecond)
	if len(store.transitions()) != 1 || len(client.closed()) != 1 {
		t.Fatalf("duplicate marker repeated completion")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerRetriesDoneBeforeClosing(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(7)}, transitionErrors: []error{errors.New("database busy"), nil}}
	client := newFakeManagerClient()
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	ticks := make(chan time.Time, 2)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.inputs()) == 1 })
	stream.events <- transport.Event{Type: "raw_output", PaneID: "ticket-coding-7", Message: "ZELLIJ_AGENT_TICKET_DONE 7"}
	waitFor(t, func() bool { return len(store.transitions()) == 1 })
	if len(client.closed()) != 0 {
		t.Fatal("pane closed before done persisted")
	}
	ticks <- time.Now()
	waitFor(t, func() bool { return len(store.transitions()) == 2 && len(client.closed()) == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerCreateFailureRequeuesClaimedTicket(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(9)}}
	client := newFakeManagerClient()
	client.createErrors = []error{errors.New("create failed")}
	client.streams = []*fakeEventStream{newFakeEventStream()}
	manager := newTestManager(t, store, client, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(store.requeues()) == 1 })
	if store.requeues()[0] != 9 {
		t.Fatalf("requeues = %#v", store.requeues())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerInputFailureClosesBeforeRequeue(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(10)}}
	client := newFakeManagerClient()
	client.inputErrors = []error{errors.New("input failed")}
	client.streams = []*fakeEventStream{newFakeEventStream()}
	client.beforeClose = func() {
		if len(store.requeues()) != 0 {
			t.Error("ticket requeued before pane close")
		}
	}
	manager := newTestManager(t, store, client, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.closed()) == 1 && len(store.requeues()) == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerCloseFailureRetainsCapacityUntilPaneAbsent(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(21), managerTicket(22)}}
	client := newFakeManagerClient()
	client.closeErrors = []error{errors.New("close failed"), errors.New("close failed"), errors.New("close failed")}
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	ticks := make(chan time.Time, 3)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.inputs()) == 1 })
	stream.events <- transport.Event{Type: "raw_output", PaneID: "ticket-coding-21", Message: "ZELLIJ_AGENT_TICKET_DONE 21"}
	waitFor(t, func() bool { return len(client.closed()) == 1 })
	ticks <- time.Now()
	waitFor(t, func() bool { return len(client.closed()) == 2 })
	if len(client.created()) != 1 {
		t.Fatalf("capacity refilled while pane present: creates=%d", len(client.created()))
	}
	client.setPaneAbsent("ticket-coding-21", true)
	ticks <- time.Now()
	waitFor(t, func() bool { return len(client.created()) == 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerCloseFailureWithRuntimeErrorStatusRetainsCapacity(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(23), managerTicket(24)}}
	client := newFakeManagerClient()
	client.closeErrors = []error{errors.New("backend close failed")}
	client.setPaneStatus("ticket-coding-23", "error")
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.inputs()) == 1 })
	stream.events <- transport.Event{Type: "raw_output", PaneID: "ticket-coding-23", Message: "ZELLIJ_AGENT_TICKET_DONE 23"}
	waitFor(t, func() bool { return len(client.closed()) == 1 })
	if len(client.created()) != 1 {
		t.Fatalf("error-status pane released capacity: creates=%d", len(client.created()))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerStreamLossPausesClaimsUntilReconnect(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(31), managerTicket(32)}}
	client := newFakeManagerClient()
	first := newFakeEventStream()
	client.streams = []*fakeEventStream{first}
	ticks := make(chan time.Time, 4)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.inputs()) == 1 })
	first.events <- transport.Event{Type: "raw_output", PaneID: "ticket-coding-31", Message: "ZELLIJ_AGENT_TICKET_DONE 31"}
	waitFor(t, func() bool { return len(client.closed()) == 1 })
	first.errs <- errors.New("stream lost")
	time.Sleep(10 * time.Millisecond)
	ticks <- time.Now()
	time.Sleep(20 * time.Millisecond)
	if len(client.created()) != 1 {
		t.Fatalf("created while disconnected: %d", len(client.created()))
	}
	client.addStream(newFakeEventStream())
	ticks <- time.Now()
	waitFor(t, func() bool { return len(client.created()) == 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerReconnectsAndRecoversMarkerFromSnapshot(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(11)}}
	client := newFakeManagerClient()
	first := newFakeEventStream()
	second := newFakeEventStream()
	client.streams = []*fakeEventStream{first, second}
	ticks := make(chan time.Time, 2)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.inputs()) == 1 })
	first.errs <- errors.New("stream lost")
	waitFor(t, func() bool { return client.streamCalls() == 1 })
	client.setSnapshot("ticket-coding-11", "work\nZELLIJ_AGENT_TICKET_DONE 11\n")
	ticks <- time.Now()
	waitFor(t, func() bool {
		return client.streamCalls() == 2 && len(store.transitions()) == 1 && len(client.closed()) == 1
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerShutdownClosesAndRequeuesActiveTicket(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(13)}}
	client := newFakeManagerClient()
	client.streams = []*fakeEventStream{newFakeEventStream()}
	manager := newTestManager(t, store, client, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.inputs()) == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() shutdown error = %v", err)
	}
	if len(client.closed()) != 1 || len(store.requeues()) != 1 || store.requeues()[0] != 13 {
		t.Fatalf("shutdown closes=%v requeues=%v", client.closed(), store.requeues())
	}
}

func newTestManager(t *testing.T, store *fakeManagerStore, client *fakeManagerClient, capacity int) *Manager {
	t.Helper()
	return newTestManagerWithTicks(t, store, client, capacity, nil)
}

func newTestManagerWithTicks(t *testing.T, store *fakeManagerStore, client *fakeManagerClient, capacity int, ticks <-chan time.Time) *Manager {
	t.Helper()
	manager, err := NewManager(ManagerOptions{
		Store: store, Client: client,
		Config: Config{Version: 1, MaxWorkers: capacity, PollInterval: time.Hour, PromptTemplate: "Ticket {{ .ID }}: {{ .Title }}"},
		Root:   "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
		StartupTimeout: 200 * time.Millisecond, ReadyPollInterval: time.Millisecond, Tick: ticks, Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func runManager(ctx context.Context, manager *Manager) <-chan error {
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	return done
}

func managerTicket(id int64) Ticket {
	return Ticket{ID: id, Title: "Ticket", Summary: "Summary", SpecPath: "docs/superpowers/specs/t-design.md", PlanPath: "docs/superpowers/plans/t.md", Status: StatusInProgress}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

type managerTransitionCall struct {
	id     int64
	action Action
}

type fakeManagerStore struct {
	mu               sync.Mutex
	ready            []Ticket
	nextCalls        int
	transitionCalls  []managerTransitionCall
	transitionErrors []error
	requeueCalls     []int64
	sequence         []string
}

func (f *fakeManagerStore) Next(context.Context) (Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextCalls++
	if len(f.ready) == 0 {
		return Ticket{}, ErrEmptyQueue
	}
	ticket := f.ready[0]
	f.ready = f.ready[1:]
	ticket.Status = StatusInProgress
	return ticket, nil
}

func (f *fakeManagerStore) Transition(_ context.Context, id int64, action Action) (Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transitionCalls = append(f.transitionCalls, managerTransitionCall{id: id, action: action})
	f.sequence = append(f.sequence, "done:"+itoa(id))
	if len(f.transitionErrors) > 0 {
		err := f.transitionErrors[0]
		f.transitionErrors = f.transitionErrors[1:]
		if err != nil {
			return Ticket{}, err
		}
	}
	return Ticket{ID: id, Status: StatusDone}, nil
}

func (f *fakeManagerStore) Requeue(_ context.Context, id int64) (Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requeueCalls = append(f.requeueCalls, id)
	return Ticket{ID: id, Status: StatusReady}, nil
}

func (f *fakeManagerStore) nextCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.nextCalls }
func (f *fakeManagerStore) transitions() []managerTransitionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]managerTransitionCall(nil), f.transitionCalls...)
}
func (f *fakeManagerStore) requeues() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.requeueCalls...)
}
func (f *fakeManagerStore) callSequence() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sequence...)
}

type fakeInput struct {
	paneID string
	req    transport.SendInputRequest
}

type fakeEventStream struct {
	events chan transport.Event
	errs   chan error
}

func newFakeEventStream() *fakeEventStream {
	return &fakeEventStream{events: make(chan transport.Event, 20), errs: make(chan error, 5)}
}

func (f *fakeEventStream) transportStream() *transport.EventStream {
	return &transport.EventStream{Events: f.events, Errors: f.errs, Close: func() error { return nil }}
}

type fakeManagerClient struct {
	mu              sync.Mutex
	anchorReady     bool
	anchorTaskID    string
	anchorSessionID string
	inspectCalls    int
	streamQueue     []*fakeEventStream
	streams         []*fakeEventStream
	streamCallN     int
	createRequests  []transport.CreatePaneRequest
	createErrors    []error
	inputRequests   []fakeInput
	inputErrors     []error
	snapshots       map[string]string
	closeRequests   []string
	closeErrors     []error
	sequence        []string
	beforeClose     func()
	absentPanes     map[string]bool
	paneStatuses    map[string]string
}

func newFakeManagerClient() *fakeManagerClient {
	return &fakeManagerClient{anchorReady: true, anchorTaskID: "tickets", anchorSessionID: "physical-a", snapshots: map[string]string{}, absentPanes: map[string]bool{}, paneStatuses: map[string]string{}}
}

func (f *fakeManagerClient) CreatePane(_ context.Context, req transport.CreatePaneRequest) (transport.CreatePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createRequests = append(f.createRequests, req)
	if len(f.createErrors) > 0 {
		err := f.createErrors[0]
		f.createErrors = f.createErrors[1:]
		if err != nil {
			return transport.CreatePaneResponse{}, err
		}
	}
	return transport.CreatePaneResponse{Pane: transport.Pane{ID: req.ID, TaskID: req.TaskID, SessionID: req.ZellijSession}}, nil
}

func (f *fakeManagerClient) SendInput(_ context.Context, paneID string, req transport.SendInputRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputRequests = append(f.inputRequests, fakeInput{paneID: paneID, req: req})
	if len(f.inputErrors) > 0 {
		err := f.inputErrors[0]
		f.inputErrors = f.inputErrors[1:]
		return err
	}
	return nil
}

func (f *fakeManagerClient) SnapshotOutput(_ context.Context, paneID string, _ transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	output, ok := f.snapshots[paneID]
	if !ok {
		output = "›"
	}
	return transport.SnapshotOutputResponse{Pane: transport.Pane{ID: paneID}, Output: output}, nil
}

func (f *fakeManagerClient) ClosePane(_ context.Context, paneID string) (transport.ClosePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.beforeClose != nil {
		f.beforeClose()
	}
	f.closeRequests = append(f.closeRequests, paneID)
	f.sequence = append(f.sequence, "close:"+paneID)
	if len(f.closeErrors) > 0 {
		err := f.closeErrors[0]
		f.closeErrors = f.closeErrors[1:]
		if err != nil {
			return transport.ClosePaneResponse{}, err
		}
	}
	return transport.ClosePaneResponse{Pane: transport.Pane{ID: paneID}}, nil
}

func (f *fakeManagerClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls++
	if !f.anchorReady {
		return transport.InspectRuntimeResponse{}, nil
	}
	panes := []transport.Pane{{ID: "ticket-manager", TaskID: f.anchorTaskID, SessionID: f.anchorSessionID, Status: "running"}}
	for _, req := range f.createRequests {
		if f.absentPanes[req.ID] {
			continue
		}
		status := f.paneStatuses[req.ID]
		if status == "" {
			status = "running"
		}
		panes = append(panes, transport.Pane{ID: req.ID, TaskID: req.TaskID, SessionID: req.ZellijSession, Status: status})
	}
	return transport.InspectRuntimeResponse{Panes: panes}, nil
}

func (f *fakeManagerClient) StreamEvents(context.Context) (*transport.EventStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streamCallN++
	if len(f.streams) == 0 {
		return nil, errors.New("no stream")
	}
	stream := f.streams[0]
	f.streams = f.streams[1:]
	return stream.transportStream(), nil
}

func (f *fakeManagerClient) setAnchorReady(value bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.anchorReady = value
}
func (f *fakeManagerClient) setSnapshot(id, output string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots[id] = output
}
func (f *fakeManagerClient) setPaneAbsent(id string, absent bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.absentPanes[id] = absent
}
func (f *fakeManagerClient) setPaneStatus(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneStatuses[id] = status
}
func (f *fakeManagerClient) addStream(stream *fakeEventStream) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams = append(f.streams, stream)
}
func (f *fakeManagerClient) inspections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inspectCalls
}
func (f *fakeManagerClient) streamCalls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.streamCallN }
func (f *fakeManagerClient) created() []transport.CreatePaneRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]transport.CreatePaneRequest(nil), f.createRequests...)
}
func (f *fakeManagerClient) inputs() []fakeInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeInput(nil), f.inputRequests...)
}
func (f *fakeManagerClient) closed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closeRequests...)
}
func (f *fakeManagerClient) callSequence() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sequence...)
}

func itoa(id int64) string {
	if id == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for id > 0 {
		i--
		digits[i] = byte('0' + id%10)
		id /= 10
	}
	return string(digits[i:])
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
