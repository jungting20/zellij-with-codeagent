package ticketworkercli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

func TestRunInitCreatesConfigAndForceReplacesIt(t *testing.T) {
	root := t.TempDir()
	cfg := testCLIConfig(root, &fakeClient{})

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init"}, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
		t.Fatalf("init code = %d, stderr=%q", code, stderr.String())
	}
	path := ticketworker.ConfigPath(root)
	if !strings.Contains(stdout.String(), path) {
		t.Fatalf("stdout = %q, want config path %q", stdout.String(), path)
	}
	if _, err := ticketworker.LoadConfig(root); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init"}, strings.NewReader(""), &stdout, &stderr, cfg); code != 1 {
		t.Fatalf("second init code = %d, want 1", code)
	}
	if err := os.WriteFile(path, []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--force"}, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
		t.Fatalf("init --force code = %d, stderr=%q", code, stderr.String())
	}
	if _, err := ticketworker.LoadConfig(root); err != nil {
		t.Fatalf("LoadConfig() after force error = %v", err)
	}
}

func TestRunStartDryRunPrintsValidatedPlanWithExplicitMaxWorkers(t *testing.T) {
	root := t.TempDir()
	writeValidConfig(t, root)
	client := &fakeClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"start", "--dry-run", "--max-workers", "5"}, strings.NewReader(""), &stdout, &stderr, testCLIConfig(root, client))

	if code != 0 {
		t.Fatalf("start --dry-run code = %d, stderr=%q", code, stderr.String())
	}
	if client.submitCalls != 0 {
		t.Fatalf("submit calls = %d, want 0", client.submitCalls)
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v; output=%q", err, stdout.String())
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Session != "ticket-worker-20260716-033456-"+cwdHash(root) {
		t.Fatalf("session = %q, want deterministic session", payload.Session)
	}
	if len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 2 {
		t.Fatalf("payload = %#v, want two-pane plan", payload)
	}
	manager, monitor := payload.Tabs[0].Panes[0], payload.Tabs[0].Panes[1]
	if !containsAdjacent(manager.Command, "--config", ticketworker.ConfigPath(root)) {
		t.Fatalf("manager command = %#v, want absolute config path", manager.Command)
	}
	if !containsAdjacent(manager.Command, "--max-workers", "5") {
		t.Fatalf("manager command = %#v, want capacity override", manager.Command)
	}
	if !containsAdjacent(monitor.Command, "--capacity", "5") {
		t.Fatalf("monitor command = %#v, want capacity override", monitor.Command)
	}
}

func TestRunStartPreservesConfiguredMaxWorkersWithoutOverride(t *testing.T) {
	root := t.TempDir()
	writeValidConfig(t, root)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"start", "--dry-run", "--session", "tickets"}, strings.NewReader(""), &stdout, &stderr, testCLIConfig(root, &fakeClient{}))
	if code != 0 {
		t.Fatalf("start code = %d, stderr=%q", code, stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !containsAdjacent(payload.Tabs[0].Panes[1].Command, "--capacity", "3") {
		t.Fatalf("monitor command = %#v, want configured capacity", payload.Tabs[0].Panes[1].Command)
	}
}

func TestRunStartRejectsMissingOrInvalidConfigBeforeCreatingClient(t *testing.T) {
	for _, tt := range []struct {
		name    string
		prepare func(*testing.T, string)
	}{
		{name: "missing"},
		{name: "invalid", prepare: func(t *testing.T, root string) {
			path := ticketworker.ConfigPath(root)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("version: 1\nworker:\n  command: []\n  completion_marker: DONE\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.prepare != nil {
				tt.prepare(t, root)
			}
			clientCreated := false
			cfg := testCLIConfig(root, &fakeClient{})
			cfg.NewClient = func(string, time.Duration) Client {
				clientCreated = true
				return &fakeClient{}
			}
			var stdout, stderr bytes.Buffer
			if code := Run([]string{"start"}, strings.NewReader(""), &stdout, &stderr, cfg); code != 1 {
				t.Fatalf("code = %d, want 1; stderr=%q", code, stderr.String())
			}
			if clientCreated {
				t.Fatal("NewClient called before config validation completed")
			}
		})
	}
}

func TestRunStartSubmitsPlan(t *testing.T) {
	root := t.TempDir()
	writeValidConfig(t, root)
	client := &fakeClient{response: transport.ExecutionPlanResponse{
		RequestID: "req_tickets", Session: "tickets", Layout: "triple-horizontal",
		Tabs: []transport.ExecutionPlanTabResponse{{Name: "ticket-worker"}},
	}}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"start", "--session", "tickets", "--socket", "/tmp/custom.sock", "--timeout", "5s"}, strings.NewReader(""), &stdout, &stderr, testCLIConfig(root, client))

	if code != 0 {
		t.Fatalf("code = %d, stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" || client.timeout != 5*time.Second || client.requestID != "req_tickets" {
		t.Fatalf("client socket=%q timeout=%s request=%q", client.socketPath, client.timeout, client.requestID)
	}
	if client.payload.Session != "tickets" || client.submitCalls != 1 {
		t.Fatalf("payload/session calls = %q/%d", client.payload.Session, client.submitCalls)
	}
	if !strings.Contains(stdout.String(), "request=req_tickets session=tickets") {
		t.Fatalf("stdout = %q, want submission summary", stdout.String())
	}
}

func TestRunManagerTreatsContextCancellationAsSuccessWithoutCleanup(t *testing.T) {
	root := t.TempDir()
	writeValidConfig(t, root)
	fake := &fakeManager{err: context.Canceled}
	var gotOptions ticketworker.ManagerOptions
	cfg := testCLIConfig(root, &fakeClient{})
	cfg.NewManager = func(opts ticketworker.ManagerOptions) (Manager, error) {
		gotOptions = opts
		return fake, nil
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"manager", "--cwd", root,
		"--config", ticketworker.ConfigPath(root),
		"--task", "tickets",
		"--anchor", "ticket-worker-manager",
	}, strings.NewReader(""), &stdout, &stderr, cfg)

	if code != 0 {
		t.Fatalf("manager code = %d, stderr=%q", code, stderr.String())
	}
	if fake.runCalls != 1 {
		t.Fatalf("Run calls = %d, want 1", fake.runCalls)
	}
	if gotOptions.TaskID != "tickets" || gotOptions.AnchorPaneID != "ticket-worker-manager" || gotOptions.CWD != root {
		t.Fatalf("manager options = %#v", gotOptions)
	}
	if !reflect.DeepEqual(gotOptions.Config.Worker.Command, []string{"go", "run", "./cmd/ticket-worker"}) {
		t.Fatalf("worker command = %#v, want direct project argv", gotOptions.Config.Worker.Command)
	}
}

func TestRunManagerAppliesExplicitMaxWorkersOverride(t *testing.T) {
	root := t.TempDir()
	writeValidConfig(t, root)
	var gotOptions ticketworker.ManagerOptions
	cfg := testCLIConfig(root, &fakeClient{})
	cfg.NewManager = func(opts ticketworker.ManagerOptions) (Manager, error) {
		gotOptions = opts
		return &fakeManager{err: context.Canceled}, nil
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"manager", "--cwd", root,
		"--config", ticketworker.ConfigPath(root),
		"--task", "tickets", "--anchor", "ticket-worker-manager",
		"--max-workers", "5",
	}, strings.NewReader(""), &stdout, &stderr, cfg)

	if code != 0 {
		t.Fatalf("manager code = %d, stderr=%q", code, stderr.String())
	}
	if gotOptions.Config.MaxWorkers != 5 {
		t.Fatalf("manager max workers = %d, want 5", gotOptions.Config.MaxWorkers)
	}
}

type fakeClient struct {
	socketPath  string
	timeout     time.Duration
	requestID   string
	payload     transport.ExecutionPlanPayload
	response    transport.ExecutionPlanResponse
	submitCalls int
}

func (c *fakeClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	c.requestID = requestID
	c.payload = payload
	c.submitCalls++
	return c.response, nil
}

type fakeManager struct {
	err      error
	runCalls int
}

func (m *fakeManager) Run(context.Context) error {
	m.runCalls++
	return m.err
}

func testCLIConfig(root string, client *fakeClient) Config {
	return Config{
		Executable: []string{"/tmp/bin/zellij-agent"},
		NewClient: func(socketPath string, timeout time.Duration) Client {
			client.socketPath = socketPath
			client.timeout = timeout
			return client
		},
		Getwd: func() (string, error) { return root, nil },
		Now: func() time.Time {
			return time.Date(2026, 7, 16, 12, 34, 56, 0, time.FixedZone("KST", 9*60*60))
		},
	}
}

func writeValidConfig(t *testing.T, root string) {
	t.Helper()
	if _, err := ticketworker.InitConfig(root, false); err != nil {
		t.Fatal(err)
	}
}

func cwdHash(cwd string) string {
	session := ticketworker.SessionID(cwd, time.Time{})
	return session[len(session)-8:]
}

func containsAdjacent(values []string, key, value string) bool {
	for i := range values {
		if i+1 < len(values) && values[i] == key && values[i+1] == value {
			return true
		}
	}
	return false
}
