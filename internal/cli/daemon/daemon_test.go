package daemoncli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

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
