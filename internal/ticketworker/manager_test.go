package ticketworker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
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
		if req.InitialInput != "" || req.InitialInputReadyText != "" {
			t.Fatalf("create[%d] terminal initialization = (%q, %q), want empty", i, req.InitialInput, req.InitialInputReadyText)
		}
		wantID := int64(i + 1)
		if req.ID != "ticket-coding-run-a-"+string(rune('0'+wantID)) || req.Name != wantNames[i] || req.Role != "coding-agent" || req.TaskID != "tickets" || req.SameTabAsPaneID != "ticket-manager" || req.ZellijSession != "physical-a" {
			t.Fatalf("create[%d] = %#v", i, req)
		}
		wantRoot := "/repo/.worktrees/ticket-" + strconv.FormatInt(wantID, 10)
		if req.CWD != wantRoot {
			t.Fatalf("create[%d] cwd = %q, want %q", i, req.CWD, wantRoot)
		}
		wantPrompt, _, err := RenderTicketPrompt(managerTicket(wantID))
		if err != nil {
			t.Fatal(err)
		}
		wantCommand := []string{"zellij-agent", "role", "coding-agent", "--yolo", wantRoot, "--", wantPrompt}
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

func TestManagerRequeuesTicketWhenWorktreePreparationFails(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(42)}}
	client := newFakeManagerClient()
	client.streams = []*fakeEventStream{newFakeEventStream()}
	manager := newTestManager(t, store, client, 1)
	manager.worktrees = fakeWorktreePreparer{err: errors.New("branch is already checked out")}

	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(store.requeues()) == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() shutdown error = %v", err)
	}
	if len(client.created()) != 0 {
		t.Fatalf("pane creates = %d, want 0", len(client.created()))
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

	prompt := client.created()[0].Command[len(client.created()[0].Command)-1]
	if !strings.HasPrefix(prompt, "Implement ticket.\n\n") {
		t.Fatalf("Codex prompt argument = %q, want stored ticket prompt prefix", prompt)
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

func TestManagerIgnoresRenderedViewportPromptEcho(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(41)}}
	client := newFakeManagerClient()
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	manager := newTestManager(t, store, client, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })

	request := client.created()[0]
	viewport := renderedPromptViewport(request.Command[len(request.Command)-1])
	stream.events <- transport.Event{
		Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-41", Message: viewport,
	}
	time.Sleep(20 * time.Millisecond)
	if got := store.transitions(); len(got) != 0 {
		t.Fatalf("rendered prompt echo completed ticket: %v", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerPeriodicSnapshotIgnoresRenderedViewportPromptEcho(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(42)}}
	client := newFakeManagerClient()
	client.streams = []*fakeEventStream{newFakeEventStream()}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })

	request := client.created()[0]
	client.setSnapshot("ticket-coding-run-a-42", renderedPromptViewport(request.Command[len(request.Command)-1]))
	ticks <- time.Now()
	time.Sleep(20 * time.Millisecond)
	if got := store.transitions(); len(got) != 0 {
		t.Fatalf("periodic rendered prompt snapshot completed ticket: %v", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerCompletesPromptEchoFollowedByRealFinalOutput(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(43)}}
	client := newFakeManagerClient()
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	manager := newTestManager(t, store, client, 1)
	manager.config.VoiceNotifications = true
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })

	request := client.created()[0]
	output := renderedPromptViewport(request.Command[len(request.Command)-1]) +
		"\nZELLIJ_AGENT_TICKET_SUMMARY 실제 완료 변경\nZELLIJ_AGENT_TICKET_DONE 43"
	stream.events <- transport.Event{
		Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-43", Message: output,
	}
	waitFor(t, func() bool { return len(client.voiceRequests()) == 1 })
	if got := client.voiceRequests()[0].Summary; got != "실제 완료 변경" {
		t.Fatalf("voice summary = %q, want 실제 완료 변경", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerCapturesSplitEventSummaryFromIdentityCheckedSnapshot(t *testing.T) {
	store := &fakeManagerStore{}
	client := newFakeManagerClient()
	_, err := client.CreatePane(context.Background(), transport.CreatePaneRequest{
		ID: "ticket-coding-run-a-42", TaskID: "tickets", ZellijSession: "physical-a", Role: "coding-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	client.setSnapshot("ticket-coding-run-a-42", "ZELLIJ_AGENT_TICKET_SUMMARY daemon owns speech\nZELLIJ_AGENT_TICKET_DONE 42\n")
	client.setSnapshotPane("ticket-coding-run-a-42", transport.Pane{
		ID: "ticket-coding-run-a-42", TaskID: "tickets", SessionID: "wrong-session", Role: "coding-agent", Status: "running",
	})
	manager := newTestManagerWithVoice(t, client, true, nil)
	manager.store = store
	manager.slots[0] = managerSlot{
		state: managerSlotWorking, ticket: managerTicket(42), paneID: "ticket-coding-run-a-42",
		marker: "ZELLIJ_AGENT_TICKET_DONE 42", paneCreated: true,
	}
	event := transport.Event{Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-42", Message: "ZELLIJ_AGENT_TICKET_DONE 42"}

	manager.handleEvent(context.Background(), event)
	if got := store.transitions(); len(got) != 0 {
		t.Fatalf("wrong-identity snapshot completed ticket: %v", got)
	}
	client.setSnapshotPane("ticket-coding-run-a-42", transport.Pane{
		ID: "ticket-coding-run-a-42", TaskID: "tickets", SessionID: "physical-a", Role: "coding-agent", Status: "running",
	})
	manager.handleEvent(context.Background(), event)

	requests := client.voiceRequests()
	if len(requests) != 1 || requests[0].Summary != "daemon owns speech" {
		t.Fatalf("voice requests = %#v, want recovered snapshot summary", requests)
	}
	if requests[0].RequestID != "tickets:42:1782864000123456789" {
		t.Fatalf("request ID = %q, want completed transition timestamp", requests[0].RequestID)
	}
}

func TestManagerQueuesCompletionWithoutSummaryWhenSnapshotHasNone(t *testing.T) {
	client := newFakeManagerClient()
	_, err := client.CreatePane(context.Background(), transport.CreatePaneRequest{
		ID: "ticket-coding-run-a-42", TaskID: "tickets", ZellijSession: "physical-a", Role: "coding-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	client.setSnapshot("ticket-coding-run-a-42", "work\nZELLIJ_AGENT_TICKET_DONE 42\n")
	manager := newTestManagerWithVoice(t, client, true, nil)
	manager.slots[0] = managerSlot{
		state: managerSlotWorking, ticket: managerTicket(42), paneID: "ticket-coding-run-a-42",
		marker: "ZELLIJ_AGENT_TICKET_DONE 42", paneCreated: true,
	}

	manager.handleEvent(context.Background(), transport.Event{
		Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-42", Message: "ZELLIJ_AGENT_TICKET_DONE 42",
	})

	requests := client.voiceRequests()
	if len(requests) != 1 || requests[0].Summary != "" {
		t.Fatalf("voice requests = %#v, want empty summary fallback", requests)
	}
	if got := client.snapshotCount("ticket-coding-run-a-42"); got != 1 {
		t.Fatalf("fallback snapshot calls = %d, want exactly 1", got)
	}
}

func TestManagerDoesNotRecoverForeignSummaryAcrossTargetMarker(t *testing.T) {
	client := newFakeManagerClient()
	_, err := client.CreatePane(context.Background(), transport.CreatePaneRequest{
		ID: "ticket-coding-run-a-42", TaskID: "tickets", ZellijSession: "physical-a", Role: "coding-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	client.setSnapshot("ticket-coding-run-a-42", strings.Join([]string{
		"ZELLIJ_AGENT_TICKET_SUMMARY foreign ticket summary",
		"ZELLIJ_AGENT_TICKET_DONE 41",
		"ZELLIJ_AGENT_TICKET_DONE 42",
	}, "\n"))
	manager := newTestManagerWithVoice(t, client, true, nil)
	manager.slots[0] = managerSlot{
		state: managerSlotWorking, ticket: managerTicket(42), paneID: "ticket-coding-run-a-42",
		marker: "ZELLIJ_AGENT_TICKET_DONE 42", paneCreated: true,
	}

	manager.handleEvent(context.Background(), transport.Event{
		Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-42", Message: "ZELLIJ_AGENT_TICKET_DONE 42",
	})

	requests := client.voiceRequests()
	if len(requests) != 1 || requests[0].Summary != "" {
		t.Fatalf("voice requests = %#v, want target completion without foreign summary", requests)
	}
}

func TestManagerMarkerEventSnapshotErrorFallsBackAfterWorkerIdentityCheck(t *testing.T) {
	store := &fakeManagerStore{}
	client := newFakeManagerClient()
	_, err := client.CreatePane(context.Background(), transport.CreatePaneRequest{
		ID: "ticket-coding-run-a-44", TaskID: "tickets", ZellijSession: "physical-a", Role: "coding-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	client.snapshotErrors["ticket-coding-run-a-44"] = errors.New("snapshot unavailable")
	manager := newTestManager(t, store, client, 1)
	var logs synchronizedBuffer
	manager.log = &logs
	manager.slots[0] = managerSlot{
		state: managerSlotWorking, ticket: managerTicket(44), paneID: "ticket-coding-run-a-44",
		marker: "ZELLIJ_AGENT_TICKET_DONE 44", paneCreated: true,
	}

	manager.handleEvent(context.Background(), transport.Event{
		Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-44", Message: "ZELLIJ_AGENT_TICKET_DONE 44",
	})

	if got := store.transitions(); len(got) != 1 || got[0].id != 44 {
		t.Fatalf("transitions = %v, want ticket 44 completion", got)
	}
	if got := client.voiceRequests(); len(got) != 0 {
		t.Fatalf("voice requests = %#v, want none while disabled", got)
	}
	if !logs.Contains("completion snapshot") || !logs.Contains("snapshot unavailable") {
		t.Fatalf("manager log = %q, want snapshot fallback failure", logs.String())
	}
	if got := client.snapshotCount("ticket-coding-run-a-44"); got != 1 {
		t.Fatalf("fallback snapshot calls = %d, want exactly 1", got)
	}
	if got := client.inspections(); got != 1 {
		t.Fatalf("runtime inspections = %d, want one independent identity check", got)
	}
}

func TestManagerMarkerEventRejectsInactiveFallbackSnapshot(t *testing.T) {
	store := &fakeManagerStore{}
	client := newFakeManagerClient()
	client.setSnapshotPane("ticket-coding-run-a-45", transport.Pane{
		ID: "ticket-coding-run-a-45", TaskID: "tickets", SessionID: "physical-a", Role: "coding-agent", Status: "exited",
	})
	manager := newTestManager(t, store, client, 1)
	manager.slots[0] = managerSlot{
		state: managerSlotWorking, ticket: managerTicket(45), paneID: "ticket-coding-run-a-45",
		marker: "ZELLIJ_AGENT_TICKET_DONE 45", paneCreated: true,
	}

	manager.handleEvent(context.Background(), transport.Event{
		Type: "raw_output", TaskID: "tickets", PaneID: "ticket-coding-run-a-45", Message: "ZELLIJ_AGENT_TICKET_DONE 45",
	})

	if got := store.transitions(); len(got) != 0 {
		t.Fatalf("inactive snapshot completed ticket: %v", got)
	}
	if got := client.snapshotCount("ticket-coding-run-a-45"); got != 1 {
		t.Fatalf("fallback snapshot calls = %d, want exactly 1", got)
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
	completed := completedManagerTicket(25)
	store := &fakeManagerStore{
		ready:            []Ticket{managerTicket(25)},
		transitionErrors: []error{ErrInvalidTransition},
		tickets:          map[int64]Ticket{25: completed},
	}
	client := newFakeManagerClient()
	stream := newFakeEventStream()
	client.streams = []*fakeEventStream{stream}
	ticks := make(chan time.Time, 1)
	manager := newTestManagerWithTicks(t, store, client, 1, ticks)
	manager.config.VoiceNotifications = true
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
	requests := client.voiceRequests()
	if len(requests) != 1 || requests[0].RequestID != "tickets:25:1782864000123456789" {
		t.Fatalf("voice requests = %#v, want stored completed_at request ID", requests)
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
	if !slices.Equal(created[1].Command, created[0].Command) || created[1].InitialInput != "" || created[1].InitialInputReadyText != "" {
		t.Fatalf("retried command = %#v, want initial command retained without terminal input", created[1])
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

func TestManagerQueuesCompletionVoiceAfterSuccessfulClose(t *testing.T) {
	client := newFakeManagerClient()
	client.beforeQueueVoice = func() {
		if len(client.closed()) == 0 {
			t.Error("voice notification queued before pane close")
		}
	}
	manager := newTestManagerWithVoice(t, client, true, nil)
	manager.slots[0] = completedManagerSlot(42, "tests passed")

	manager.retryClose(context.Background(), &manager.slots[0])

	want := transport.VoiceNotificationRequest{
		RequestID: "tickets:42:1782864000123456789",
		Prefix:    "ticket-manager",
		TicketID:  42,
		Summary:   "tests passed",
	}
	if got := client.voiceRequests(); !reflect.DeepEqual(got, []transport.VoiceNotificationRequest{want}) {
		t.Fatalf("voice requests = %#v, want %#v", got, []transport.VoiceNotificationRequest{want})
	}
	if deadlines := client.voiceDeadlines(); len(deadlines) != 1 || deadlines[0].IsZero() || time.Until(deadlines[0]) > time.Second {
		t.Fatalf("voice deadlines = %v, want one deadline within one second", deadlines)
	}
	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
	}
}

func TestCompletionVoiceRequestIDChangesAfterReopenedTicketCompletesAgain(t *testing.T) {
	first := completedManagerTicket(42)
	second := completedManagerTicket(42)
	reopenedCompletedAt := first.CompletedAt.Add(time.Second)
	second.CompletedAt = &reopenedCompletedAt

	firstID, err := completionVoiceRequestID("tickets", first)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := completionVoiceRequestID("tickets", second)
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatalf("request IDs = %q and %q, want reopened completion to differ", firstID, secondID)
	}
}

func TestCompletionVoiceRequestIDPreservesRawFormatAndHashesOnlyWhenNeeded(t *testing.T) {
	ticket := completedManagerTicket(42)
	tests := []struct {
		name   string
		taskID string
		want   string
	}{
		{
			name:   "documented raw format",
			taskID: "tickets",
			want:   "tickets:42:1782864000123456789",
		},
		{
			name:   "raw format at endpoint byte limit",
			taskID: strings.Repeat("t", 233),
			want:   strings.Repeat("t", 233) + ":42:1782864000123456789",
		},
		{
			name:   "deterministic hash fallback above endpoint byte limit",
			taskID: strings.Repeat("task/", 60),
			want:   "sha256:4f1da0aa79b2921d3f4bd44add8eb746975fd7482b5a92fb013a987ab7e65383:42:1782864000123456789",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := completionVoiceRequestID(tt.taskID, ticket)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("completionVoiceRequestID() = %q, want %q", got, tt.want)
			}
			if len(got) > 256 {
				t.Fatalf("request ID length = %d, want at most 256 bytes", len(got))
			}
			again, err := completionVoiceRequestID(tt.taskID, ticket)
			if err != nil || again != got {
				t.Fatalf("second completionVoiceRequestID() = (%q, %v), want deterministic %q", again, err, got)
			}
		})
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
	manager := newTestManagerWithVoice(t, client, true, nil)
	manager.slots[0] = completedManagerSlot(42, "tests passed")

	manager.retryClose(context.Background(), &manager.slots[0])
	if got := client.voiceRequests(); len(got) != 0 {
		t.Fatalf("voice requests after failed close = %#v, want none", got)
	}
	if manager.slots[0].state != managerSlotClosing {
		t.Fatalf("slot state after failed close = %v, want closing", manager.slots[0].state)
	}

	manager.retryClose(context.Background(), &manager.slots[0])
	if got := client.voiceRequests(); len(got) != 1 {
		t.Fatalf("voice requests after close retry = %#v, want one", got)
	}
	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state after retry = %v, want empty", manager.slots[0].state)
	}
}

func TestManagerVoiceNotificationRetriesStableRequestWithExactBackoff(t *testing.T) {
	client := newFakeManagerClient()
	client.voiceErrors = []error{
		errors.New("connection reset after request"),
		&transport.ClientError{APIError: transport.APIError{Code: transport.CodeTimeout, Message: "busy", Retryable: true}},
		nil,
	}
	client.voiceResponses = []transport.VoiceNotificationResponse{{}, {}, {Status: "duplicate"}}
	var delays []time.Duration
	manager := newTestManagerWithVoice(t, client, true, func(ctx context.Context, delay time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		delays = append(delays, delay)
		return nil
	})
	manager.slots[0] = completedManagerSlot(42, "tests passed")

	manager.retryClose(context.Background(), &manager.slots[0])

	requests := client.voiceRequests()
	if len(requests) != 3 {
		t.Fatalf("voice requests = %d, want 3", len(requests))
	}
	for i := 1; i < len(requests); i++ {
		if requests[i].RequestID != requests[0].RequestID {
			t.Fatalf("request IDs = %q, %q, want stable", requests[0].RequestID, requests[i].RequestID)
		}
	}
	if !reflect.DeepEqual(delays, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}) {
		t.Fatalf("backoff delays = %v, want [100ms 200ms]", delays)
	}
	for _, deadline := range client.voiceDeadlines() {
		remaining := time.Until(deadline)
		if deadline.IsZero() || remaining <= 900*time.Millisecond || remaining > time.Second {
			t.Fatalf("voice deadline remaining = %v, want one-second per-attempt context", remaining)
		}
	}
	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
	}
}

func TestManagerVoiceNotificationDoesNotRetryNonRetryableHTTP400(t *testing.T) {
	client := newFakeManagerClient()
	client.voiceErrors = []error{&transport.ClientError{
		StatusCode: http.StatusBadRequest,
		APIError:   transport.APIError{Code: transport.CodeBadRequest, Message: "invalid request"},
	}}
	var backoffCalls int
	manager := newTestManagerWithVoice(t, client, true, func(context.Context, time.Duration) error {
		backoffCalls++
		return nil
	})
	var logs synchronizedBuffer
	manager.log = &logs
	manager.slots[0] = completedManagerSlot(42, "tests passed")

	manager.retryClose(context.Background(), &manager.slots[0])

	if got := len(client.voiceRequests()); got != 1 {
		t.Fatalf("voice requests = %d, want 1", got)
	}
	if backoffCalls != 0 {
		t.Fatalf("backoff calls = %d, want 0", backoffCalls)
	}
	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
	}
	if !logs.Contains("notify ticket=42") || !logs.Contains("invalid request") {
		t.Fatalf("manager log = %q, want final notification failure", logs.String())
	}
}

func TestManagerVoiceNotificationRetriesHTTP5xxWithoutRetryableFlag(t *testing.T) {
	client := newFakeManagerClient()
	client.voiceErrors = []error{
		&transport.ClientError{
			StatusCode: http.StatusInternalServerError,
			APIError:   transport.APIError{Code: transport.CodeRuntimeError, Message: "daemon failed"},
		},
		nil,
	}
	var delays []time.Duration
	manager := newTestManagerWithVoice(t, client, true, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	})
	manager.slots[0] = completedManagerSlot(42, "tests passed")

	manager.retryClose(context.Background(), &manager.slots[0])

	if got := len(client.voiceRequests()); got != 2 {
		t.Fatalf("voice requests = %d, want 2", got)
	}
	if !reflect.DeepEqual(delays, []time.Duration{100 * time.Millisecond}) {
		t.Fatalf("backoff delays = %v, want [100ms]", delays)
	}
	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
	}
}

func TestManagerVoiceNotificationExhaustionClearsSlot(t *testing.T) {
	client := newFakeManagerClient()
	client.voiceErrors = []error{errors.New("network 1"), errors.New("network 2"), errors.New("network 3")}
	var delays []time.Duration
	manager := newTestManagerWithVoice(t, client, true, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	})
	var logs synchronizedBuffer
	manager.log = &logs
	manager.slots[0] = completedManagerSlot(42, "")

	manager.retryClose(context.Background(), &manager.slots[0])

	if got := len(client.voiceRequests()); got != 3 {
		t.Fatalf("voice requests = %d, want 3", got)
	}
	if !reflect.DeepEqual(delays, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}) {
		t.Fatalf("backoff delays = %v, want [100ms 200ms]", delays)
	}
	if manager.slots[0].state != managerSlotEmpty {
		t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
	}
	if !logs.Contains("network 3") {
		t.Fatalf("manager log = %q, want final failure", logs.String())
	}
}

func TestManagerVoiceNotificationDisabledAndMissingCompletedAtClearSlot(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		ticket  Ticket
	}{
		{name: "disabled", enabled: false, ticket: completedManagerTicket(42)},
		{name: "missing completed at", enabled: true, ticket: managerTicket(42)},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newFakeManagerClient()
			manager := newTestManagerWithVoice(t, client, test.enabled, nil)
			var logs synchronizedBuffer
			manager.log = &logs
			manager.slots[0] = completedManagerSlot(42, "tests passed")
			manager.slots[0].ticket = test.ticket

			manager.retryClose(context.Background(), &manager.slots[0])

			if got := client.voiceRequests(); len(got) != 0 {
				t.Fatalf("voice requests = %#v, want none", got)
			}
			if manager.slots[0].state != managerSlotEmpty {
				t.Fatalf("slot state = %v, want empty", manager.slots[0].state)
			}
			if test.enabled && !logs.Contains("completed ticket is missing completed_at") {
				t.Fatalf("manager log = %q, want missing completed_at", logs.String())
			}
		})
	}
}

func TestManagerShutdownUsesLiveContextForDoneNotification(t *testing.T) {
	store := &fakeManagerStore{}
	client := newFakeManagerClient()
	client.streams = []*fakeEventStream{newFakeEventStream()}
	manager, err := NewManager(ManagerOptions{
		Store: store, Client: client,
		Config: Config{Version: 1, MaxWorkers: 2, PollInterval: time.Hour, VoiceNotifications: true, VoiceNotificationPrefix: defaultVoiceNotificationPrefix},
		Root:   "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
		StartupTimeout: 200 * time.Millisecond, ReadyPollInterval: time.Millisecond, Log: io.Discard, ManagerID: "run-a",
		NotificationBackoff: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.slots[0] = completedManagerSlot(42, "tests passed")
	manager.slots[1] = managerSlot{
		state: managerSlotWorking, ticket: managerTicket(43), paneID: "ticket-coding-run-a-43", paneCreated: true,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return client.streamCalls() == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := len(client.voiceRequests()); got != 1 {
		t.Fatalf("voice requests = %d, want 1", got)
	}
	if canceled := client.voiceContextErrors(); len(canceled) != 1 || canceled[0] != nil {
		t.Fatalf("voice context errors = %v, want live cleanup context", canceled)
	}
	deadlines := client.voiceDeadlines()
	if len(deadlines) != 1 || deadlines[0].IsZero() {
		t.Fatalf("voice deadlines = %v, want one cleanup deadline", deadlines)
	}
	remaining := time.Until(deadlines[0])
	if remaining <= 0 || remaining > manager.startupTimeout {
		t.Fatalf("cleanup deadline remaining = %v, want positive and bounded by %v", remaining, manager.startupTimeout)
	}
	if got := store.requeues(); !reflect.DeepEqual(got, []int64{43}) {
		t.Fatalf("requeued tickets = %v, want [43]", got)
	}
}

func TestNewManagerTrimsVoiceNotificationPrefix(t *testing.T) {
	manager, err := NewManager(ManagerOptions{
		Store: &fakeManagerStore{}, Client: newFakeManagerClient(),
		Config: Config{Version: 1, MaxWorkers: 1, PollInterval: time.Hour, VoiceNotifications: true, VoiceNotificationPrefix: " project-a "},
		Root:   "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := manager.config.VoiceNotificationPrefix; got != "project-a" {
		t.Fatalf("voice notification prefix = %q, want project-a", got)
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
	manager.config.VoiceNotifications = true
	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(client.created()) == 1 })
	client.setSnapshot("ticket-coding-run-a-12", "work\nZELLIJ_AGENT_TICKET_SUMMARY periodic recovery\nZELLIJ_AGENT_TICKET_DONE 12\n")
	ticks <- time.Now()
	waitFor(t, func() bool { return len(store.transitions()) == 1 && len(client.closed()) == 1 })
	if client.streamCalls() != 1 {
		t.Fatalf("stream calls = %d, want existing stream retained", client.streamCalls())
	}
	requests := client.voiceRequests()
	if len(requests) != 1 || requests[0].Summary != "periodic recovery" {
		t.Fatalf("voice requests = %#v, want periodic snapshot summary", requests)
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
		Worktrees: fakeWorktreePreparer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func newTestManagerWithVoice(t *testing.T, client *fakeManagerClient, enabled bool, backoff NotificationBackoff) *Manager {
	t.Helper()
	manager, err := NewManager(ManagerOptions{
		Store: &fakeManagerStore{}, Client: client,
		Config: Config{Version: 1, MaxWorkers: 1, PollInterval: time.Hour, VoiceNotifications: enabled, VoiceNotificationPrefix: defaultVoiceNotificationPrefix},
		Root:   "/repo", TaskID: "tickets", AnchorPaneID: "ticket-manager", ZellijSession: "physical-a", RoleBin: "zellij-agent",
		StartupTimeout: 200 * time.Millisecond, ReadyPollInterval: time.Millisecond, Log: io.Discard, ManagerID: "run-a",
		NotificationBackoff: backoff, Worktrees: fakeWorktreePreparer{},
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
	return Ticket{ID: id, Title: "Ticket", Summary: "Summary", SpecPath: "docs/superpowers/specs/t-design.md", PlanPath: "docs/superpowers/plans/t.md", WorktreeBranch: "ticket/" + strconv.FormatInt(id, 10), Prompt: "Implement ticket.", Status: StatusInProgress}
}

type fakeWorktreePreparer struct{ err error }

func (f fakeWorktreePreparer) Prepare(_ context.Context, root string, ticket Ticket) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return root + "/.worktrees/ticket-" + strconv.FormatInt(ticket.ID, 10), nil
}

func completedManagerTicket(id int64) Ticket {
	ticket := managerTicket(id)
	completedAt := time.Date(2026, time.July, 1, 0, 0, 0, 123456789, time.UTC)
	ticket.Status = StatusDone
	ticket.CompletedAt = &completedAt
	return ticket
}

func completedManagerSlot(id int64, summary string) managerSlot {
	return managerSlot{
		state:       managerSlotClosing,
		ticket:      completedManagerTicket(id),
		paneID:      "ticket-coding-run-a-" + strconv.FormatInt(id, 10),
		paneCreated: true,
		done:        true,
		summary:     summary,
	}
}

func renderedPromptViewport(prompt string) string {
	return "terminal header\n• " + strings.ReplaceAll(strings.TrimSpace(prompt), "\n", "\n• ") + "\n›"
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
	return completedManagerTicket(id), nil
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
	snapshotCalls     map[string]int
	closeRequests     []string
	closeErrors       []error
	beforeClose       func()
	absentPanes       map[string]bool
	paneStatuses      map[string]string
	voiceRequestsLog  []transport.VoiceNotificationRequest
	voiceResponses    []transport.VoiceNotificationResponse
	voiceErrors       []error
	voiceDeadlineLog  []time.Time
	voiceCtxErrors    []error
	beforeQueueVoice  func()
}

func newFakeManagerClient() *fakeManagerClient {
	return &fakeManagerClient{anchorReady: true, anchorTaskID: "tickets", anchorSessionID: "physical-a", successfulCreates: map[string]bool{}, snapshots: map[string]string{}, snapshotPanes: map[string]transport.Pane{}, snapshotErrors: map[string]error{}, snapshotCalls: map[string]int{}, absentPanes: map[string]bool{}, paneStatuses: map[string]string{}}
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
	f.snapshotCalls[paneID]++
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

func (f *fakeManagerClient) QueueVoiceNotification(ctx context.Context, req transport.VoiceNotificationRequest) (transport.VoiceNotificationResponse, error) {
	if f.beforeQueueVoice != nil {
		f.beforeQueueVoice()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.voiceRequestsLog = append(f.voiceRequestsLog, req)
	deadline, _ := ctx.Deadline()
	f.voiceDeadlineLog = append(f.voiceDeadlineLog, deadline)
	f.voiceCtxErrors = append(f.voiceCtxErrors, ctx.Err())
	var response transport.VoiceNotificationResponse
	if len(f.voiceResponses) > 0 {
		response = f.voiceResponses[0]
		f.voiceResponses = f.voiceResponses[1:]
	} else {
		response.Status = "queued"
	}
	if len(f.voiceErrors) > 0 {
		err := f.voiceErrors[0]
		f.voiceErrors = f.voiceErrors[1:]
		if err != nil {
			return transport.VoiceNotificationResponse{}, err
		}
	}
	return response, nil
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
func (f *fakeManagerClient) voiceRequests() []transport.VoiceNotificationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]transport.VoiceNotificationRequest(nil), f.voiceRequestsLog...)
}
func (f *fakeManagerClient) voiceDeadlines() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.voiceDeadlineLog...)
}
func (f *fakeManagerClient) voiceContextErrors() []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]error(nil), f.voiceCtxErrors...)
}
func (f *fakeManagerClient) snapshotCount(paneID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshotCalls[paneID]
}
