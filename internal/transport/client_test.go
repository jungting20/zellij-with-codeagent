package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

func TestClientCreatePaneOverUnixSocket(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	response, err := client.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-1",
		TaskID:  "task-1",
		Role:    "test",
		NewTab:  true,
		TabName: "agentd-test",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if response.Pane.ID != "pane-1" || response.Pane.ZellijTabID == nil || *response.Pane.ZellijTabID != 7 {
		t.Fatalf("CreatePane() = %#v, want pane metadata", response.Pane)
	}
}

func TestClientReturnsStructuredTransportError(t *testing.T) {
	service := newFakeRuntimeService()
	service.sendErr = rt.ErrPaneNotFound
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	err := client.SendInput(context.Background(), "missing", SendInputRequest{Text: "noop"})
	var clientErr *ClientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("SendInput() error = %T %v, want ClientError", err, err)
	}
	if clientErr.APIError.Code != CodeNotFound || clientErr.StatusCode != 404 {
		t.Fatalf("ClientError = %#v, want not_found 404", clientErr)
	}
}

func TestClientStreamsEvents(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.StreamEvents(ctx)
	if err != nil {
		t.Fatalf("StreamEvents() error = %v", err)
	}
	defer stream.Close()

	service.publish(eventbus.Event{Type: eventbus.TypeTestPassed, PaneID: "test", Message: "ok", Time: time.Unix(1, 0)})

	select {
	case event := <-stream.Events:
		if event.Type != string(eventbus.TypeTestPassed) || event.PaneID != "test" {
			t.Fatalf("event = %#v, want test_passed for test", event)
		}
	case err := <-stream.Errors:
		t.Fatalf("stream error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for streamed event")
	}
}

func TestClientRecentEvents(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	response, err := client.RecentEvents(context.Background(), 1, string(eventbus.TypeTestPassed))
	if err != nil {
		t.Fatalf("RecentEvents() error = %v", err)
	}
	if len(response.Events) != 1 || response.Events[0].Type != string(eventbus.TypeTestPassed) {
		t.Fatalf("RecentEvents() = %#v, want one test_passed event", response)
	}
	if service.recentReq.Limit != 1 {
		t.Fatalf("runtime recent limit = %d, want 1", service.recentReq.Limit)
	}
}

func startUnixTransport(t *testing.T, service *fakeRuntimeService) (*Client, func()) {
	t.Helper()
	socketPath := shortSocketPath(t)
	server, err := NewServer(ServerOptions{
		Service:        service,
		SocketPath:     socketPath,
		RequestTimeout: time.Second,
		Version:        "test",
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()

	client := NewClient(ClientOptions{SocketPath: socketPath, Timeout: time.Second})
	deadline := time.After(time.Second)
	for {
		if _, err := client.Health(context.Background()); err == nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("timed out waiting for unix transport health")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cleanup := func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("ListenAndServe() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out stopping transport server")
		}
	}
	return client, cleanup
}
