package transport_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	fakeplanner "zellij-with-codeagent/internal/planner/fake"
	"zellij-with-codeagent/internal/registry"
	rt "zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/transport"
	"zellij-with-codeagent/internal/zellij"
)

func TestE2EFakePlannerOverUnixTransport(t *testing.T) {
	if os.Getenv("AGENTD_TRANSPORT_E2E") != "1" {
		t.Skip("set AGENTD_TRANSPORT_E2E=1 to run fake planner through agentd transport against real Zellij")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	taskID := rt.TaskID("transport-e2e-task")
	backend := zellij.NewBackend(zellij.Options{Session: os.Getenv("ZELLIJ_SESSION_NAME")})
	service := rt.NewService(rt.Options{
		Registry:           registry.New(),
		Backend:            backend,
		SubscriptionRunner: rt.ExecSubscriptionRunner{},
	})
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = service.Cleanup(cleanupCtx, rt.CleanupRequest{TaskID: taskID})
	}()

	socketPath := shortSocketPath(t)
	server, err := transport.NewServer(transport.ServerOptions{
		Service:        service,
		SocketPath:     socketPath,
		RequestTimeout: 10 * time.Second,
		Version:        "e2e",
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	serverCtx, serverCancel := context.WithCancel(ctx)
	defer serverCancel()
	serverErrs := make(chan error, 1)
	go func() {
		serverErrs <- server.ListenAndServe(serverCtx)
	}()

	client := transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: 10 * time.Second})
	waitForHealth(ctx, t, client)

	planner := fakeplanner.New(client, fakeplanner.Options{
		TaskID:       string(taskID),
		AgentID:      "transport-e2e-agent",
		TabName:      "agentd-transport-e2e",
		CWD:          ".",
		IdleTimeout:  20 * time.Second,
		TotalTimeout: 45 * time.Second,
	})
	result, err := planner.Run(ctx)
	if err != nil {
		t.Fatalf("planner Run() error = %v; result=%#v", err, result)
	}
	if len(result.Panes) != 5 {
		t.Fatalf("planner created %d panes, want 5", len(result.Panes))
	}
	if len(result.Events) != 3 {
		t.Fatalf("planner observed events = %#v, want server/test lifecycle events", result.Events)
	}
	if result.Cleanup == nil || len(result.Cleanup.Closed) == 0 {
		t.Fatalf("cleanup = %#v, want managed panes closed", result.Cleanup)
	}

	serverCancel()
	select {
	case err := <-serverErrs:
		if err != nil && err != context.Canceled {
			t.Fatalf("transport server error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out stopping transport server")
	}
}

func waitForHealth(ctx context.Context, t *testing.T, client *transport.Client) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := client.Health(ctx); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for transport health: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	path := fmt.Sprintf("/tmp/agentd-e2e-%d.sock", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = os.Remove(path)
	})
	return path
}
