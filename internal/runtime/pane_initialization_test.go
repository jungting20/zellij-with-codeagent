package runtime

import (
	"context"
	"reflect"
	"sync"
	"testing"

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
