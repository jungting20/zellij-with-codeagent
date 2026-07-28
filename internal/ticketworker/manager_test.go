package ticketworker

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
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
	waitFor(t, func() bool { return len(client.created()) == 2 })
	if store.nextCount() != 2 {
		t.Fatalf("Next calls = %d, want 2", store.nextCount())
	}
	wantNames := []string{"[1] Ticket", "[2] Ticket"}
	for i, req := range client.created() {
		if req.InitialInputReadyText != "›" {
			t.Fatalf("create[%d] ready text = %q", i, req.InitialInputReadyText)
		}
		if !strings.HasSuffix(req.InitialInput, "\n") ||
			!strings.Contains(req.InitialInput, "Implement ticket.") {
			t.Fatalf("create[%d] initial input = %q", i, req.InitialInput)
		}
		wantID := int64(i + 1)
		if req.ID != "ticket-coding-run-a-"+string(rune('0'+wantID)) || req.Name != wantNames[i] || req.Role != "coding-agent" || req.TaskID != "tickets" || req.SameTabAsPaneID != "ticket-manager" || req.ZellijSession != "physical-a" {
			t.Fatalf("create[%d] = %#v", i, req)
		}
		wantCommand := []string{"zellij-agent", "role", "coding-agent", "--yolo", "/repo"}
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
				Config: Config{Version: 1, MaxWorkers: 1, PollInterval: time.Hour, VoiceNotifications: false, VoiceNotificationPrefix: defaultVoiceNotificationPrefix},
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

func TestNewManagerGeneratesDistinctInstanceIDs(t *testing.T) {
	store := &fakeManagerStore{}
	client := newFakeManagerClient()
	opts := ManagerOptions{
		Store: store, Client: client,
		Config: Config{Version: 1, MaxWorkers: 1, PollInterval: time.Hour, VoiceNotifications: false, VoiceNotificationPrefix: defaultVoiceNotificationPrefix},
		Root:   "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
	}
	first, err := NewManager(opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager(opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.managerID == second.managerID || !validManagerID(first.managerID) || !validManagerID(second.managerID) {
		t.Fatalf("manager IDs = %q, %q", first.managerID, second.managerID)
	}
}

func TestManagerLogTicketfIncludesQuotedTitle(t *testing.T) {
	var output strings.Builder
	manager := &Manager{log: &output}
	ticket := Ticket{ID: 7, Title: "첫째 \"제목\"\n둘째"}

	manager.logTicketf("started", ticket, "pane=%s", "pane-7")

	want := "started ticket=7 title=\"첫째 \\\"제목\\\"\\n둘째\" pane=pane-7\n"
	if got := output.String(); got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}

func TestWorkerPaneNameNormalizesAndLimitsTitle(t *testing.T) {
	tests := []struct {
		name   string
		ticket Ticket
		want   string
	}{
		{name: "plain", ticket: Ticket{ID: 7, Title: "검색 기능 구현"}, want: "[7] 검색 기능 구현"},
		{name: "whitespace", ticket: Ticket{ID: 8, Title: "  검색\n\t기능 \r 구현  "}, want: "[8] 검색 기능 구현"},
		{name: "long unicode", ticket: Ticket{ID: 9, Title: strings.Repeat("한", 32) + "끝"}, want: "[9] " + strings.Repeat("한", 31) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerPaneName(tt.ticket); got != tt.want {
				t.Fatalf("workerPaneName(%#v) = %q, want %q", tt.ticket, got, tt.want)
			}
		})
	}
}

func TestManagerIgnoresPromptEchoAndCompletesExactMarker(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(42)}}
	client := newFakeManagerClient()
	var closeBeforeDone atomic.Bool
	client.beforeClose = func() {
		if len(store.transitions()) == 0 {
			closeBeforeDone.Store(true)
		}
	}
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	manager := newTestManager(t, store, client, 1)
	var logs synchronizedBuffer
	manager.log = &logs
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })

	prompt := client.created()[0].InitialInput
	if len(prompt) == 0 || prompt[len(prompt)-1] != '\n' {
		t.Fatalf("submitted prompt = %q, want trailing newline to send Enter", prompt)
	}
	if !strings.HasPrefix(prompt, "Implement ticket.\n\n") {
		t.Fatalf("submitted prompt = %q, want stored ticket prompt prefix", prompt)
	}
	stream.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-42", Message: prompt}
	time.Sleep(20 * time.Millisecond)
	if len(store.transitions()) != 0 || len(client.closed()) != 0 {
		t.Fatalf("prompt echo completed ticket: transitions=%v closes=%v", store.transitions(), client.closed())
	}
	for _, output := range []string{
		"prefix ZELLIJ_AGENT_TICKET_DONE 42",
		"ZELLIJ_AGENT_TICKET_DONE 42 suffix",
		"ZELLIJ_AGENT_TICKET_DONE 43",
	} {
		stream.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-42", Message: output}
	}
	time.Sleep(20 * time.Millisecond)
	if len(store.transitions()) != 0 {
		t.Fatalf("non-exact output completed ticket: %v", store.transitions())
	}
	stream.events <- transport.Event{Type: "raw_output", TaskID: "wrong-task", PaneID: "ticket-coding-run-a-42", Message: "ZELLIJ_AGENT_TICKET_DONE 42"}
	time.Sleep(20 * time.Millisecond)
	if len(store.transitions()) != 0 {
		t.Fatalf("wrong-task event completed ticket: %v", store.transitions())
	}

	stream.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-42", Message: "work\n  ZELLIJ_AGENT_TICKET_DONE 42  \n"}
	waitFor(t, func() bool { return len(store.transitions()) == 1 && len(client.closed()) == 1 })
	if call := store.transitions()[0]; call.id != 42 || call.action != ActionDone {
		t.Fatalf("transition = %#v", call)
	}
	if closeBeforeDone.Load() {
		t.Fatal("coding-agent pane closed before ticket reached done")
	}
	for _, want := range []string{
		`started ticket=42 title="Ticket" pane=ticket-coding-run-a-42`,
		`closed ticket=42 title="Ticket" pane=ticket-coding-run-a-42`,
	} {
		waitFor(t, func() bool { return logs.Contains(want) })
		if !logs.Contains(want) {
			t.Fatalf("manager log %q does not contain %q", logs.String(), want)
		}
	}
	stream.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-42", Message: "ZELLIJ_AGENT_TICKET_DONE 42"}
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
	var logs synchronizedBuffer
	manager.log = &logs
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	stream.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-7", Message: "ZELLIJ_AGENT_TICKET_DONE 7"}
	waitFor(t, func() bool { return len(store.transitions()) == 1 })
	wantLog := `complete ticket=7 title="Ticket" failed: database busy`
	waitFor(t, func() bool { return logs.Contains(wantLog) })
	if !logs.Contains(wantLog) {
		t.Fatalf("manager log %q does not contain %q", logs.String(), wantLog)
	}
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

func TestManagerClosesPaneWhenTicketIsAlreadyDone(t *testing.T) {
	store := &fakeManagerStore{
		ready:            []Ticket{managerTicket(25)},
		transitionErrors: []error{ErrInvalidTransition},
		tickets:          map[int64]Ticket{25: {ID: 25, Status: StatusDone}},
	}
	client := newFakeManagerClient()
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })

	stream.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-25", Message: "ZELLIJ_AGENT_TICKET_DONE 25"}
	waitFor(t, func() bool { return len(store.transitions()) == 1 })
	waitFor(t, func() bool { return len(client.closed()) == 1 })
	ticks <- time.Now()
	time.Sleep(20 * time.Millisecond)
	if got := len(store.transitions()); got != 1 {
		t.Fatalf("already-done ticket retried transition: %d calls", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerSafeCreateFailureRequeuesClaimedTicket(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(9)}}
	client := newFakeManagerClient()
	client.createErrors = []error{&transport.ClientError{APIError: transport.APIError{Code: transport.CodeBadRequest, Message: "invalid target"}}}
	client.streams = []*fakeEventStream{newFakeEventStream()}
	manager := newTestManager(t, store, client, 1)
	var logs synchronizedBuffer
	manager.log = &logs
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(store.requeues()) == 1 })
	wantLog := `create ticket=9 title="Ticket" pane=ticket-coding-run-a-9 failed:`
	waitFor(t, func() bool { return logs.Contains(wantLog) })
	if !logs.Contains(wantLog) {
		t.Fatalf("manager log %q does not contain %q", logs.String(), wantLog)
	}
	if store.requeues()[0] != 9 {
		t.Fatalf("requeues = %#v", store.requeues())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerUncertainCreateFailureRetriesSamePaneThenRequeues(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(15)}}
	client := newFakeManagerClient()
	client.createErrors = []error{errors.New("connection reset after request")}
	client.streams = []*fakeEventStream{newFakeEventStream()}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	if len(store.requeues()) != 0 {
		t.Fatalf("uncertain create was requeued: %v", store.requeues())
	}
	ticks <- time.Now()
	waitFor(t, func() bool {
		return len(client.created()) == 2 && len(client.closed()) == 1 && len(store.requeues()) == 1
	})
	created := client.created()
	if created[0].ID != created[1].ID {
		t.Fatalf("retried pane IDs = %q, %q", created[0].ID, created[1].ID)
	}
	if created[0].InitialInput == "" || created[1].InitialInput != created[0].InitialInput ||
		created[1].InitialInputReadyText != created[0].InitialInputReadyText {
		t.Fatalf("retried initialization = %#v, want initial request retained", created[1])
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerUncertainRecoveryInitializationFailureRequeuesWithoutClose(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(17)}}
	client := newFakeManagerClient()
	client.createErrors = []error{
		errors.New("connection reset after request"),
		&transport.ClientError{APIError: transport.APIError{
			Code: transport.CodeInitializationFailed, Message: "prompt failed", Retryable: true,
		}},
	}
	client.streams = []*fakeEventStream{newFakeEventStream()}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)

	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	ticks <- time.Now()
	waitFor(t, func() bool { return len(store.requeues()) == 1 })
	if len(client.created()) != 2 {
		t.Fatalf("creates = %d, want initial create and one recovery retry", len(client.created()))
	}
	if len(client.closed()) != 0 {
		t.Fatalf("closed panes = %v, want none", client.closed())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerUncertainRecoveryCreateUsesStartupTimeout(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(18)}}
	client := newFakeManagerClient()
	client.createErrors = []error{errors.New("connection reset after request")}
	client.streams = []*fakeEventStream{newFakeEventStream()}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)

	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	ticks <- time.Now()
	waitFor(t, func() bool { return len(client.created()) == 2 && len(store.requeues()) == 1 })
	deadlines := client.createDeadlines()
	if len(deadlines) != 2 || deadlines[1].IsZero() {
		t.Fatalf("create deadlines = %v, want bounded recovery deadline", deadlines)
	}
	if latest := time.Now().Add(manager.startupTimeout); deadlines[1].After(latest) {
		t.Fatalf("recovery deadline = %v, want no later than %v", deadlines[1], latest)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerUncertainCreateFailureCleansPaneCreatedBeforeResponseLoss(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(16)}}
	client := newFakeManagerClient()
	client.createErrors = []error{errors.New("connection reset after create")}
	client.createOnError = true
	client.streams = []*fakeEventStream{newFakeEventStream()}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	ticks <- time.Now()
	waitFor(t, func() bool { return len(client.closed()) == 1 && len(store.requeues()) == 1 })
	if len(client.created()) != 1 {
		t.Fatalf("existing pane should be discovered before retry: creates=%d", len(client.created()))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerInitializationFailureRequeuesWithoutClose(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(10)}}
	client := newFakeManagerClient()
	client.createErrors = []error{&transport.ClientError{APIError: transport.APIError{
		Code: transport.CodeInitializationFailed, Message: "prompt failed", Retryable: true,
	}}}
	client.streams = []*fakeEventStream{newFakeEventStream()}
	manager := newTestManager(t, store, client, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(store.requeues()) == 1 })
	if len(client.closed()) != 0 {
		t.Fatalf("closed panes = %v, want none", client.closed())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerCleanupPartialKeepsUncertainRecovery(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(10)}}
	client := newFakeManagerClient()
	client.createErrors = []error{&transport.ClientError{APIError: transport.APIError{
		Code: transport.CodeCleanupPartial, Message: "cleanup failed", Retryable: true,
	}}}
	client.createOnError = true
	client.streams = []*fakeEventStream{newFakeEventStream()}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)

	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	inspectionsBeforeRecovery := client.inspections()
	ticks <- time.Now()
	waitFor(t, func() bool { return len(client.closed()) == 1 && len(store.requeues()) == 1 })
	if client.inspections() <= inspectionsBeforeRecovery {
		t.Fatal("uncertain create was not discovered before cleanup")
	}
	if len(client.created()) != 1 {
		t.Fatalf("creates = %d, want no retry after discovery", len(client.created()))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerCreateUsesStartupTimeout(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(10)}}
	client := newFakeManagerClient()
	manager := newTestManager(t, store, client, 1)

	manager.startSlot(context.Background(), &manager.slots[0])

	deadlines := client.createDeadlines()
	if len(deadlines) != 1 || deadlines[0].IsZero() {
		t.Fatalf("create deadlines = %v, want one deadline", deadlines)
	}
	if latest := time.Now().Add(manager.startupTimeout); deadlines[0].After(latest) {
		t.Fatalf("create deadline = %v, want no later than %v", deadlines[0], latest)
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
	waitFor(t, func() bool { return len(client.created()) == 1 })
	stream.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-21", Message: "ZELLIJ_AGENT_TICKET_DONE 21"}
	waitFor(t, func() bool { return len(client.closed()) == 1 })
	ticks <- time.Now()
	waitFor(t, func() bool { return len(client.closed()) == 2 })
	if len(client.created()) != 1 {
		t.Fatalf("capacity refilled while pane present: creates=%d", len(client.created()))
	}
	client.setPaneAbsent("ticket-coding-run-a-21", true)
	ticks <- time.Now()
	waitFor(t, func() bool { return len(client.created()) == 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerVoiceNotificationAfterSuccessfulClose(t *testing.T) {
	client := newFakeManagerClient()
	notifier := &recordingVoiceNotifier{}
	manager := newTestManagerWithVoiceNotifier(t, client, true, notifier)
	manager.slots[0] = managerSlot{
		state:       managerSlotClosing,
		ticket:      managerTicket(42),
		paneID:      "ticket-coding-run-a-42",
		paneCreated: true,
		done:        true,
	}

	manager.retryClose(context.Background(), &manager.slots[0])

	if got := notifier.recordedMessages(); len(got) != 1 || got[0] != "ticket-manager:42:완료" {
		t.Fatalf("notifications = %q, want [ticket-manager:42:완료]", got)
	}
	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
	}
}

func TestManagerVoiceNotificationsDisabled(t *testing.T) {
	client := newFakeManagerClient()
	notifier := &recordingVoiceNotifier{}
	manager := newTestManagerWithVoiceNotifier(t, client, false, notifier)
	manager.slots[0] = managerSlot{
		state:       managerSlotClosing,
		ticket:      managerTicket(42),
		paneID:      "ticket-coding-run-a-42",
		paneCreated: true,
		done:        true,
	}

	manager.retryClose(context.Background(), &manager.slots[0])

	if got := notifier.recordedMessages(); len(got) != 0 {
		t.Fatalf("notifications = %q, want none", got)
	}
}

func TestManagerVoiceNotificationWaitsForSuccessfulCloseRetry(t *testing.T) {
	client := newFakeManagerClient()
	client.closeErrors = []error{errors.New("close failed"), nil}
	_, err := client.CreatePane(context.Background(), transport.CreatePaneRequest{
		ID: "ticket-coding-run-a-42", TaskID: "tickets", ZellijSession: "physical-a", Role: "coding-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	notifier := &recordingVoiceNotifier{}
	manager := newTestManagerWithVoiceNotifier(t, client, true, notifier)
	manager.slots[0] = managerSlot{
		state:       managerSlotClosing,
		ticket:      managerTicket(42),
		paneID:      "ticket-coding-run-a-42",
		paneCreated: true,
		done:        true,
	}

	manager.retryClose(context.Background(), &manager.slots[0])
	if got := notifier.recordedMessages(); len(got) != 0 {
		t.Fatalf("notifications after failed close = %q, want none", got)
	}
	if manager.slots[0].state != managerSlotClosing {
		t.Fatalf("slot state after failed close = %v, want closing", manager.slots[0].state)
	}

	manager.retryClose(context.Background(), &manager.slots[0])
	if got := notifier.recordedMessages(); len(got) != 1 || got[0] != "ticket-manager:42:완료" {
		t.Fatalf("notifications after retry = %q, want one completion", got)
	}
	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state after retry = %v, want empty", manager.slots[0].state)
	}
}

func TestManagerNotifyErrorDoesNotRetainCompletedSlot(t *testing.T) {
	client := newFakeManagerClient()
	notifier := &recordingVoiceNotifier{notifyErr: errors.New("voice unavailable")}
	manager := newTestManagerWithVoiceNotifier(t, client, true, notifier)
	var logs synchronizedBuffer
	manager.log = &logs
	manager.slots[0] = managerSlot{
		state:       managerSlotClosing,
		ticket:      managerTicket(42),
		paneID:      "ticket-coding-run-a-42",
		paneCreated: true,
		done:        true,
	}

	manager.retryClose(context.Background(), &manager.slots[0])

	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
	}
	if !logs.Contains("notify ticket=42") || !logs.Contains("failed: voice unavailable") {
		t.Fatalf("manager log = %q, want notify failure", logs.String())
	}
}

func TestManagerVoiceNotifierClosesOnceOnCancellation(t *testing.T) {
	client := newFakeManagerClient()
	client.streams = []*fakeEventStream{newFakeEventStream()}
	notifier := &recordingVoiceNotifier{}
	manager := newTestManagerWithVoiceNotifier(t, client, true, notifier)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return client.streamCalls() == 1 })

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := notifier.recordedCloseCalls(); got != 1 {
		t.Fatalf("notifier Close calls = %d, want 1", got)
	}
}

func TestNewManagerRequiresVoiceNotifierWhenEnabled(t *testing.T) {
	_, err := NewManager(ManagerOptions{
		Store: &fakeManagerStore{}, Client: newFakeManagerClient(),
		Config: Config{Version: 1, MaxWorkers: 1, PollInterval: time.Hour, VoiceNotifications: true, VoiceNotificationPrefix: defaultVoiceNotificationPrefix},
		Root:   "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
	})
	if err == nil || err.Error() != "ticket manager voice notifier is required" {
		t.Fatalf("NewManager() error = %v, want ticket manager voice notifier is required", err)
	}
}

func TestManagerCloseFailureWithRuntimeErrorStatusRetainsCapacity(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(23), managerTicket(24)}}
	client := newFakeManagerClient()
	client.closeErrors = []error{errors.New("backend close failed")}
	client.setPaneStatus("ticket-coding-run-a-23", "error")
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	stream.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-23", Message: "ZELLIJ_AGENT_TICKET_DONE 23"}
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
	waitFor(t, func() bool { return len(client.created()) == 1 })
	first.events <- transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-31", Message: "ZELLIJ_AGENT_TICKET_DONE 31"}
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
	waitFor(t, func() bool { return len(client.created()) == 1 })
	first.errs <- errors.New("stream lost")
	waitFor(t, func() bool { return client.streamCalls() == 1 })
	client.setSnapshot("ticket-coding-run-a-11", "work\nZELLIJ_AGENT_TICKET_DONE 11\n")
	ticks <- time.Now()
	waitFor(t, func() bool {
		return client.streamCalls() == 2 && len(store.transitions()) == 1 && len(client.closed()) == 1
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerRecoversDroppedMarkerEventFromPeriodicSnapshot(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(12)}}
	client := newFakeManagerClient()
	client.streams = []*fakeEventStream{newFakeEventStream()}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	client.setSnapshot("ticket-coding-run-a-12", "work\nZELLIJ_AGENT_TICKET_DONE 12\n")
	ticks <- time.Now()
	waitFor(t, func() bool { return len(store.transitions()) == 1 && len(client.closed()) == 1 })
	if client.streamCalls() != 1 {
		t.Fatalf("stream calls = %d, want existing stream retained", client.streamCalls())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerRejectsMarkerSnapshotWithWrongWorkerIdentity(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(14)}}
	client := newFakeManagerClient()
	client.streams = []*fakeEventStream{newFakeEventStream()}
	ticks := make(chan time.Time, 2)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	client.setSnapshot("ticket-coding-run-a-14", "ZELLIJ_AGENT_TICKET_DONE 14\n")
	client.setSnapshotPane("ticket-coding-run-a-14", transport.Pane{ID: "ticket-coding-run-a-14", TaskID: "tickets", SessionID: "wrong-session", Role: "coding-agent", Status: "running"})
	ticks <- time.Now()
	time.Sleep(20 * time.Millisecond)
	if len(store.transitions()) != 0 {
		t.Fatalf("wrong-session snapshot completed ticket: %v", store.transitions())
	}
	client.setSnapshotPane("ticket-coding-run-a-14", transport.Pane{ID: "ticket-coding-run-a-14", TaskID: "tickets", SessionID: "physical-a", Role: "coding-agent", Status: "running"})
	ticks <- time.Now()
	waitFor(t, func() bool { return len(store.transitions()) == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerReleasesMissingWorkerWhenTicketCannotBeRequeued(t *testing.T) {
	store := &fakeManagerStore{requeueErrors: []error{ErrInvalidTransition}}
	client := newFakeManagerClient()
	paneID := "ticket-coding-run-a-15"
	client.snapshotErrors[paneID] = &transport.ClientError{
		StatusCode: 404,
		APIError:   transport.APIError{Code: transport.CodeNotFound, Message: "runtime pane not found"},
	}
	manager := newTestManager(t, store, client, 1)
	manager.slots[0] = managerSlot{
		state:       managerSlotWorking,
		ticket:      managerTicket(15),
		paneID:      paneID,
		paneCreated: true,
	}

	manager.recoverSnapshots(context.Background())

	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
	}
	if got := client.closed(); len(got) != 1 || got[0] != paneID {
		t.Fatalf("closed panes = %v, want [%s]", got, paneID)
	}
	if got := store.requeues(); len(got) != 1 || got[0] != 15 {
		t.Fatalf("requeued tickets = %v, want [15]", got)
	}
}

func TestManagerDoesNotCreateDuplicateWorkerForClaimedTicket(t *testing.T) {
	ticket := managerTicket(16)
	store := &fakeManagerStore{ready: []Ticket{ticket}}
	client := newFakeManagerClient()
	manager := newTestManager(t, store, client, 2)
	manager.slots[0] = managerSlot{
		state:       managerSlotWorking,
		ticket:      ticket,
		paneID:      "ticket-coding-run-a-16",
		paneCreated: true,
	}

	manager.startSlot(context.Background(), &manager.slots[1])

	if got := client.created(); len(got) != 0 {
		t.Fatalf("created panes = %v, want none", got)
	}
	if manager.slots[1].state != managerSlotEmpty {
		t.Fatalf("duplicate slot state = %v, want empty", manager.slots[1].state)
	}
}

func TestManagerShutdownClosesAndRequeuesActiveTicket(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(13)}}
	client := newFakeManagerClient()
	client.streams = []*fakeEventStream{newFakeEventStream()}
	manager := newTestManager(t, store, client, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
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
		Config: Config{Version: 1, MaxWorkers: capacity, PollInterval: time.Hour, VoiceNotifications: false, VoiceNotificationPrefix: defaultVoiceNotificationPrefix},
		Root:   "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
		StartupTimeout: 200 * time.Millisecond, ReadyPollInterval: time.Millisecond, Tick: ticks, Log: io.Discard, ManagerID: "run-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func newTestManagerWithVoiceNotifier(t *testing.T, client *fakeManagerClient, enabled bool, notifier VoiceNotifier) *Manager {
	t.Helper()
	manager, err := NewManager(ManagerOptions{
		Store: &fakeManagerStore{}, Client: client,
		Config:        Config{Version: 1, MaxWorkers: 1, PollInterval: time.Hour, VoiceNotifications: enabled, VoiceNotificationPrefix: defaultVoiceNotificationPrefix},
		VoiceNotifier: notifier,
		Root:          "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
		StartupTimeout: 200 * time.Millisecond, ReadyPollInterval: time.Millisecond, Log: io.Discard, ManagerID: "run-a",
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
	return Ticket{ID: id, Title: "Ticket", Summary: "Summary", SpecPath: "docs/superpowers/specs/t-design.md", PlanPath: "docs/superpowers/plans/t.md", Prompt: "Implement ticket.", Status: StatusInProgress}
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

type synchronizedBuffer struct {
	mu      sync.Mutex
	builder strings.Builder
}

type recordingVoiceNotifier struct {
	mu         sync.Mutex
	messages   []string
	notifyErr  error
	closeCalls int
	closeErr   error
}

func (n *recordingVoiceNotifier) Notify(message string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.messages = append(n.messages, message)
	return n.notifyErr
}

func (n *recordingVoiceNotifier) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closeCalls++
	return n.closeErr
}

func (n *recordingVoiceNotifier) recordedMessages() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.messages...)
}

func (n *recordingVoiceNotifier) recordedCloseCalls() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.closeCalls
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.String()
}

func (b *synchronizedBuffer) Contains(substring string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Contains(b.builder.String(), substring)
}

type managerTransitionCall struct {
	id     int64
	action Action
}

type fakeManagerStore struct {
	mu               sync.Mutex
	ready            []Ticket
	tickets          map[int64]Ticket
	nextCalls        int
	transitionCalls  []managerTransitionCall
	transitionErrors []error
	requeueCalls     []int64
	requeueErrors    []error
}

func (f *fakeManagerStore) Get(_ context.Context, id int64) (Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ticket, ok := f.tickets[id]
	if !ok {
		return Ticket{}, ErrNotFound
	}
	return ticket, nil
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
	if len(f.requeueErrors) > 0 {
		err := f.requeueErrors[0]
		f.requeueErrors = f.requeueErrors[1:]
		if err != nil {
			return Ticket{}, err
		}
	}
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
	mu                sync.Mutex
	anchorReady       bool
	anchorTaskID      string
	anchorSessionID   string
	inspectCalls      int
	streamQueue       []*fakeEventStream
	streams           []*fakeEventStream
	streamCallN       int
	createRequests    []transport.CreatePaneRequest
	createDeadlineLog []time.Time
	successfulCreates map[string]bool
	createErrors      []error
	createOnError     bool
	snapshots         map[string]string
	snapshotPanes     map[string]transport.Pane
	snapshotErrors    map[string]error
	closeRequests     []string
	closeErrors       []error
	beforeClose       func()
	absentPanes       map[string]bool
	paneStatuses      map[string]string
}

func newFakeManagerClient() *fakeManagerClient {
	return &fakeManagerClient{anchorReady: true, anchorTaskID: "tickets", anchorSessionID: "physical-a", successfulCreates: map[string]bool{}, snapshots: map[string]string{}, snapshotPanes: map[string]transport.Pane{}, snapshotErrors: map[string]error{}, absentPanes: map[string]bool{}, paneStatuses: map[string]string{}}
}

func (f *fakeManagerClient) CreatePane(ctx context.Context, req transport.CreatePaneRequest) (transport.CreatePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createRequests = append(f.createRequests, req)
	deadline, _ := ctx.Deadline()
	f.createDeadlineLog = append(f.createDeadlineLog, deadline)
	if len(f.createErrors) > 0 {
		err := f.createErrors[0]
		f.createErrors = f.createErrors[1:]
		if err != nil {
			if f.createOnError {
				f.successfulCreates[req.ID] = true
			}
			return transport.CreatePaneResponse{}, err
		}
	}
	f.successfulCreates[req.ID] = true
	return transport.CreatePaneResponse{Pane: transport.Pane{ID: req.ID, TaskID: req.TaskID, SessionID: req.ZellijSession}}, nil
}

func (f *fakeManagerClient) SnapshotOutput(_ context.Context, paneID string, _ transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.snapshotErrors[paneID]; err != nil {
		return transport.SnapshotOutputResponse{}, err
	}
	output, ok := f.snapshots[paneID]
	if !ok {
		output = "›"
	}
	pane, ok := f.snapshotPanes[paneID]
	if !ok {
		pane = transport.Pane{ID: paneID, TaskID: "tickets", SessionID: "physical-a", Role: "coding-agent", Status: "running"}
	}
	return transport.SnapshotOutputResponse{Pane: pane, Output: output}, nil
}

func (f *fakeManagerClient) ClosePane(_ context.Context, paneID string) (transport.ClosePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.beforeClose != nil {
		f.beforeClose()
	}
	f.closeRequests = append(f.closeRequests, paneID)
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
		if !f.successfulCreates[req.ID] {
			continue
		}
		if f.absentPanes[req.ID] {
			continue
		}
		status := f.paneStatuses[req.ID]
		if status == "" {
			status = "running"
		}
		panes = append(panes, transport.Pane{ID: req.ID, TaskID: req.TaskID, SessionID: req.ZellijSession, Role: req.Role, Status: status})
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
func (f *fakeManagerClient) setSnapshotPane(id string, pane transport.Pane) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotPanes[id] = pane
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
func (f *fakeManagerClient) createDeadlines() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.createDeadlineLog...)
}
func (f *fakeManagerClient) closed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closeRequests...)
}
