package daemoncli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
	"zellij-with-codeagent/internal/voice"
)

func TestVoiceQueueAdapterConvertsNotificationAndStatuses(t *testing.T) {
	request := transport.VoiceNotificationRequest{
		RequestID: "request-42",
		Prefix:    "ticket-manager",
		TicketID:  42,
		Summary:   "tests passed",
	}
	cases := []struct {
		name   string
		status voice.EnqueueStatus
		want   string
	}{
		{name: "queued", status: voice.EnqueueStatusQueued, want: "queued"},
		{name: "duplicate", status: voice.EnqueueStatusDuplicate, want: "duplicate"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeDaemonVoiceService{enqueueStatus: tt.status}
			adapter := voiceQueueAdapter{service: service}

			response, err := adapter.QueueVoiceNotification(context.Background(), request)

			if err != nil {
				t.Fatalf("QueueVoiceNotification() error = %v", err)
			}
			if response != (transport.VoiceNotificationResponse{Status: tt.want}) {
				t.Fatalf("QueueVoiceNotification() response = %#v, want status %q", response, tt.want)
			}
			if got := service.notification(); got != (voice.Notification{
				RequestID: "request-42",
				Prefix:    "ticket-manager",
				TicketID:  42,
				Summary:   "tests passed",
			}) {
				t.Fatalf("Enqueue() notification = %#v, want converted request", got)
			}
		})
	}
}

func TestVoiceQueueAdapterMapsQueueFull(t *testing.T) {
	service := &fakeDaemonVoiceService{enqueueErr: voice.ErrQueueFull}
	adapter := voiceQueueAdapter{service: service}

	_, err := adapter.QueueVoiceNotification(context.Background(), transport.VoiceNotificationRequest{RequestID: "request-1"})

	if !errors.Is(err, transport.ErrVoiceQueueFull) {
		t.Fatalf("QueueVoiceNotification() error = %v, want transport.ErrVoiceQueueFull", err)
	}
}

func TestVoiceQueueAdapterRejectsCanceledContext(t *testing.T) {
	service := &fakeDaemonVoiceService{enqueueStatus: voice.EnqueueStatusQueued}
	adapter := voiceQueueAdapter{service: service}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := adapter.QueueVoiceNotification(ctx, transport.VoiceNotificationRequest{RequestID: "request-1"})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("QueueVoiceNotification() error = %v, want context.Canceled", err)
	}
	if got := service.enqueueCalls(); got != 0 {
		t.Fatalf("Enqueue() calls = %d, want 0", got)
	}
}

func TestRunContextClosesVoiceServiceAfterSocketShutdown(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/agentd-voice-daemon-%d.sock", time.Now().UnixNano())
	defer os.Remove(socketPath)
	service := &fakeDaemonVoiceService{closeSocketPath: socketPath}
	originalFactory := newDaemonVoiceService
	newDaemonVoiceService = func(io.Writer) daemonVoiceService { return service }
	t.Cleanup(func() { newDaemonVoiceService = originalFactory })

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	var code int
	done := make(chan struct{})
	go func() {
		code = RunContext(ctx, []string{"serve", "--socket", socketPath}, &stdout, &stderr)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("RunContext() did not stop during test cleanup")
		}
	})
	client := transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: 100 * time.Millisecond})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := client.Health(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for daemon socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	select {
	case <-done:
		if code != 0 {
			t.Fatalf("RunContext() exit code = %d, want 0; stderr=%q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("RunContext() did not stop after shutdown")
	}
	if got := service.closeCalls(); got != 1 {
		t.Fatalf("Close() calls = %d, want 1", got)
	}
	if service.socketExistedWhenClosed() {
		t.Fatal("voice service closed before the daemon socket shut down")
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after daemon shutdown: %v", err)
	}
}

type fakeDaemonVoiceService struct {
	mu                     sync.Mutex
	enqueueStatus          voice.EnqueueStatus
	enqueueErr             error
	enqueued               voice.Notification
	enqueueCallCount       int
	closeCallCount         int
	closeSocketPath        string
	socketExistedAtClosing bool
}

func (f *fakeDaemonVoiceService) Enqueue(notification voice.Notification) (voice.EnqueueStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = notification
	f.enqueueCallCount++
	return f.enqueueStatus, f.enqueueErr
}

func (f *fakeDaemonVoiceService) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCallCount++
	if f.closeSocketPath != "" {
		_, err := os.Stat(f.closeSocketPath)
		f.socketExistedAtClosing = err == nil
	}
	return nil
}

func (f *fakeDaemonVoiceService) notification() voice.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enqueued
}

func (f *fakeDaemonVoiceService) enqueueCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enqueueCallCount
}

func (f *fakeDaemonVoiceService) closeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCallCount
}

func (f *fakeDaemonVoiceService) socketExistedWhenClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.socketExistedAtClosing
}

func TestRunStopFallsBackForDaemonWithoutShutdownRoute(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "agentd.sock")
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("create socket placeholder: %v", err)
	}
	originalRequest := requestDaemonShutdown
	originalLegacy := stopLegacyDaemon
	t.Cleanup(func() {
		requestDaemonShutdown = originalRequest
		stopLegacyDaemon = originalLegacy
	})
	requestDaemonShutdown = func(context.Context, string, time.Duration) error {
		return &transport.ClientError{APIError: transport.APIError{Code: transport.CodeNotFound, Message: "route not found"}}
	}
	legacyCalled := false
	stopLegacyDaemon = func(_ context.Context, gotSocketPath string) error {
		legacyCalled = true
		if gotSocketPath != socketPath {
			t.Fatalf("legacy socket path = %q, want %q", gotSocketPath, socketPath)
		}
		return os.Remove(gotSocketPath)
	}
	var stdout, stderr bytes.Buffer

	code := RunContext(context.Background(), []string{"stop", "--socket", socketPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("RunContext() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !legacyCalled {
		t.Fatal("legacy stop fallback was not called")
	}
}

func TestRunStopDoesNotFallBackForOtherShutdownErrors(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "agentd.sock")
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("create socket placeholder: %v", err)
	}
	originalRequest := requestDaemonShutdown
	originalLegacy := stopLegacyDaemon
	t.Cleanup(func() {
		requestDaemonShutdown = originalRequest
		stopLegacyDaemon = originalLegacy
	})
	requestDaemonShutdown = func(context.Context, string, time.Duration) error {
		return errors.New("connection failed")
	}
	stopLegacyDaemon = func(context.Context, string) error {
		t.Fatal("legacy stop fallback should not be called")
		return nil
	}
	var stdout, stderr bytes.Buffer

	code := RunContext(context.Background(), []string{"stop", "--socket", socketPath}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("RunContext() exit code = %d, want 1", code)
	}
}
