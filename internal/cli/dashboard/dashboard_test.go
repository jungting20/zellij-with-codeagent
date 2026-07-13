package dashboardcli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/dashboard"
	"zellij-with-codeagent/internal/transport"
)

func TestRunForwardsOptionsAndRunsProgram(t *testing.T) {
	client := &fakeClient{}
	var gotSocket string
	var gotTimeout time.Duration
	var gotOptions dashboard.Options
	var ran bool
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--socket", "/tmp/custom.sock",
		"--timeout", "7s",
		"--refresh-interval", "3s",
		"--event-limit", "25",
	}, strings.NewReader(""), &stdout, &stderr, func(socket string, timeout time.Duration) dashboard.Client {
		gotSocket, gotTimeout = socket, timeout
		return client
	}, Config{
		NewModel: func(_ context.Context, got dashboard.Client, opts dashboard.Options) tea.Model {
			if got != client {
				t.Fatalf("client = %T, want fake", got)
			}
			gotOptions = opts
			return stubModel{}
		},
		RunProgram: func(_ context.Context, _ tea.Model, stdin io.Reader, output io.Writer) error {
			ran = true
			if stdin == nil || output != &stdout {
				t.Fatalf("io forwarding stdin=%v output=%T", stdin, output)
			}
			return nil
		},
	})

	if code != 0 || !ran || stderr.Len() != 0 {
		t.Fatalf("code=%d ran=%v stderr=%q", code, ran, stderr.String())
	}
	if gotSocket != "/tmp/custom.sock" || gotTimeout != 7*time.Second {
		t.Fatalf("client socket=%q timeout=%s", gotSocket, gotTimeout)
	}
	if gotOptions.RefreshInterval != 3*time.Second || gotOptions.EventLimit != 25 {
		t.Fatalf("options = %#v", gotOptions)
	}
}

func TestRunHelpAndValidation(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		var stdout, stderr bytes.Buffer
		code := Run(args, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeClient{}), Config{})
		if code != 0 || !strings.Contains(stdout.String(), "Usage: zellij-agent dashboard") || !strings.Contains(stdout.String(), "--refresh-interval") || stderr.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}

	for _, args := range [][]string{
		{"positional"},
		{"--unknown"},
		{"--refresh-interval", "0s"},
		{"--event-limit", "0"},
		{"--timeout", "0s"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(args, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeClient{}), Config{})
		if code != 2 || stderr.Len() == 0 {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr.String())
		}
	}
}

func TestRunProgramErrorReturnsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(nil, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeClient{}), Config{
		NewModel:   func(context.Context, dashboard.Client, dashboard.Options) tea.Model { return stubModel{} },
		RunProgram: func(context.Context, tea.Model, io.Reader, io.Writer) error { return errors.New("terminal broke") },
	})
	if code != 1 || !strings.Contains(stderr.String(), "dashboard failed: terminal broke") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

type stubModel struct{}

func (stubModel) Init() tea.Cmd                       { return nil }
func (stubModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return stubModel{}, nil }
func (stubModel) View() string                        { return "" }

type fakeClient struct{}

func (*fakeClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	return transport.InspectRuntimeResponse{}, nil
}
func (*fakeClient) RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error) {
	return transport.RecentEventsResponse{}, nil
}
func (*fakeClient) StreamEvents(context.Context) (*transport.EventStream, error) { return nil, nil }
func (*fakeClient) SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	return transport.SnapshotOutputResponse{}, nil
}
func (*fakeClient) SendInput(context.Context, string, transport.SendInputRequest) error { return nil }
func (*fakeClient) Reconcile(context.Context) (transport.ReconcileResponse, error) {
	return transport.ReconcileResponse{}, nil
}
func (*fakeClient) Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error) {
	return transport.CleanupResponse{}, nil
}

func fakeFactory(client dashboard.Client) ClientFactory {
	return func(string, time.Duration) dashboard.Client { return client }
}
