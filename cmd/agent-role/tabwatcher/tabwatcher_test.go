package tabwatcher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/transport"
)

func TestParseOptionsDefaults(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions(nil) error = %v", err)
	}
	if opts.Port != 9222 {
		t.Fatalf("Port = %d, want 9222", opts.Port)
	}
	if opts.SocketPath != "/tmp/agentd.sock" {
		t.Fatalf("SocketPath = %q, want /tmp/agentd.sock", opts.SocketPath)
	}
	if opts.Session != "chrome-tabs" {
		t.Fatalf("Session = %q, want chrome-tabs", opts.Session)
	}
	if opts.ZellijSession != "physical-a" {
		t.Fatalf("ZellijSession = %q, want physical-a", opts.ZellijSession)
	}
	if opts.RoleBin != "zellij-agent" {
		t.Fatalf("RoleBin = %q, want zellij-agent", opts.RoleBin)
	}
	if opts.UserDataDir != defaultUserDataDir {
		t.Fatalf("UserDataDir = %q, want %q", opts.UserDataDir, defaultUserDataDir)
	}
	if !opts.LaunchChrome {
		t.Fatal("LaunchChrome = false, want true")
	}
	if opts.PollInterval != 500*time.Millisecond {
		t.Fatalf("PollInterval = %s, want 500ms", opts.PollInterval)
	}
}

func TestParseOptionsAcceptsZellijSessionAndCustomValues(t *testing.T) {
	opts, err := parseOptions([]string{
		"--port", "9333",
		"--socket", "/tmp/custom.sock",
		"--cwd", "/repo",
		"--session", "chrome-debug",
		"--zellij-session", " physical-a ",
		"--role-bin", "/tmp/bin/zellij-agent",
		"--chrome-path", "/Applications/Chrome",
		"--user-data-dir", "/tmp/profile",
		"--no-launch",
		"--poll-interval", "250ms",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if opts.Port != 9333 || opts.SocketPath != "/tmp/custom.sock" || opts.CWD != "/repo" || opts.Session != "chrome-debug" {
		t.Fatalf("options = %#v, want custom port/socket/cwd/session", opts)
	}
	if opts.ZellijSession != "physical-a" {
		t.Fatalf("ZellijSession = %q, want physical-a", opts.ZellijSession)
	}
	if opts.RoleBin != "/tmp/bin/zellij-agent" || opts.ChromePath != "/Applications/Chrome" || opts.UserDataDir != "/tmp/profile" {
		t.Fatalf("options = %#v, want custom executable paths", opts)
	}
	if opts.LaunchChrome {
		t.Fatal("LaunchChrome = true, want false")
	}
	if opts.PollInterval != 250*time.Millisecond {
		t.Fatalf("PollInterval = %s, want 250ms", opts.PollInterval)
	}
}

func TestParseOptionsRejectsMissingZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	_, err := parseOptions(nil)
	if !errors.Is(err, cli.ErrZellijSessionRequired) {
		t.Fatalf("parseOptions() error = %v, want %v", err, cli.ErrZellijSessionRequired)
	}
}

func TestBuildTargetPlanPreservesZellijSessionForTarget(t *testing.T) {
	cfg := watcherConfig{
		Port:          9333,
		CWD:           "/repo",
		Session:       "chrome-tabs",
		RoleBin:       "/tmp/bin/zellij-agent",
		SocketPath:    "/tmp/agentd.sock",
		ZellijSession: "physical-a",
	}

	requestID, payload := buildTargetPlan(cfg, PageTarget{ID: "ABCDEF1234567890", Type: "page", URL: "https://example.com"})

	if requestID != "req_chrome-tab-network-ABCDEF123456" {
		t.Fatalf("requestID = %q, want target-specific request id", requestID)
	}
	if payload.Session != "chrome-tabs" || payload.Layout != "single-tab" || len(payload.Tabs) != 1 {
		t.Fatalf("payload = %#v, want one-tab chrome-tabs plan", payload)
	}
	if payload.ZellijSession != "physical-a" {
		t.Fatalf("payload.ZellijSession = %q, want physical-a", payload.ZellijSession)
	}
	if payload.Tabs[0].Name != "chrome:ABCDEF123456" {
		t.Fatalf("tab name = %q, want chrome:ABCDEF123456", payload.Tabs[0].Name)
	}
	pane := payload.Tabs[0].Panes[0]
	if pane.ID != "chrome-tab-network-ABCDEF123456" || pane.Role != "tab-network" || pane.CWD != "/repo" {
		t.Fatalf("pane = %#v, want deterministic tab-network pane in cwd", pane)
	}
	wantCommand := []string{"/tmp/bin/zellij-agent", "role", "tab-network", "--port", "9333", "--no-launch", "--target-id", "ABCDEF1234567890", "--zellij-session", "physical-a"}
	if !reflect.DeepEqual(pane.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", pane.Command, wantCommand)
	}
}

func TestTargetTrackerMarksStartupTargetsAsBaseline(t *testing.T) {
	submitter := &fakeSubmitter{}
	tracker := newTargetTracker(watcherConfig{Port: 9222, Session: "chrome-tabs", RoleBin: "zellij-agent"}, submitter, io.Discard, io.Discard)

	tracker.MarkBaseline([]PageTarget{{ID: "existing", Type: "page"}})
	tracker.ProcessTargets(context.Background(), []PageTarget{{ID: "existing", Type: "page"}})

	if len(submitter.requests) != 0 {
		t.Fatalf("submitted %d requests, want 0 for baseline target", len(submitter.requests))
	}
}

func TestTargetTrackerSubmitsNewPageTargetOnce(t *testing.T) {
	submitter := &fakeSubmitter{}
	var stdout bytes.Buffer
	tracker := newTargetTracker(watcherConfig{Port: 9333, CWD: "/repo", Session: "chrome-tabs", RoleBin: "/tmp/bin/zellij-agent"}, submitter, &stdout, io.Discard)

	target := PageTarget{ID: "new-target-123456", Type: "page", URL: "https://example.com"}
	tracker.MarkBaseline(nil)
	tracker.ProcessTargets(context.Background(), []PageTarget{target})
	tracker.ProcessTargets(context.Background(), []PageTarget{target})

	if len(submitter.requests) != 1 {
		t.Fatalf("submitted %d requests, want exactly 1", len(submitter.requests))
	}
	got := submitter.requests[0]
	if got.requestID != "req_chrome-tab-network-new-target-1" {
		t.Fatalf("requestID = %q, want target-specific id", got.requestID)
	}
	if !strings.Contains(stdout.String(), "submitted target=new-target-123456") {
		t.Fatalf("stdout = %q, want submitted target log", stdout.String())
	}
}

func TestTargetTrackerIgnoresNonPageTargets(t *testing.T) {
	submitter := &fakeSubmitter{}
	tracker := newTargetTracker(watcherConfig{Port: 9222, Session: "chrome-tabs", RoleBin: "zellij-agent"}, submitter, io.Discard, io.Discard)

	tracker.MarkBaseline(nil)
	tracker.ProcessTargets(context.Background(), []PageTarget{{ID: "worker", Type: "service_worker"}})

	if len(submitter.requests) != 0 {
		t.Fatalf("submitted %d requests, want 0 for non-page target", len(submitter.requests))
	}
}

func TestTargetTrackerLogsSubmitFailureAndDoesNotRetrySameTarget(t *testing.T) {
	submitter := &fakeSubmitter{err: errors.New("daemon down")}
	var stderr bytes.Buffer
	tracker := newTargetTracker(watcherConfig{Port: 9222, Session: "chrome-tabs", RoleBin: "zellij-agent"}, submitter, io.Discard, &stderr)

	target := PageTarget{ID: "target-fail", Type: "page"}
	tracker.ProcessTargets(context.Background(), []PageTarget{target})
	tracker.ProcessTargets(context.Background(), []PageTarget{target})

	if len(submitter.requests) != 1 {
		t.Fatalf("submitted %d requests, want no retry for same target", len(submitter.requests))
	}
	if !strings.Contains(stderr.String(), "submit target=target-fail failed") {
		t.Fatalf("stderr = %q, want failure log", stderr.String())
	}
}

func TestChromeArgsUseRemoteDebuggingAndProfile(t *testing.T) {
	got := chromeArgs(9333, "/tmp/profile")
	want := []string{
		"--remote-debugging-port=9333",
		"--user-data-dir=/tmp/profile",
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chromeArgs() = %#v, want %#v", got, want)
	}
}

func TestRunWatcherBaselinesThenSubmitsLaterTargets(t *testing.T) {
	source := &fakeTargetSource{
		batches: [][]PageTarget{
			{{ID: "existing", Type: "page"}},
			{{ID: "existing", Type: "page"}, {ID: "new-page", Type: "page"}},
		},
	}
	submitter := &fakeSubmitter{}
	ctx, cancel := context.WithCancel(context.Background())
	source.afterBatch = func(batch int) {
		if batch == 1 {
			cancel()
		}
	}

	err := runWatcher(ctx, watcherConfig{
		Port:         9222,
		Session:      "chrome-tabs",
		RoleBin:      "zellij-agent",
		PollInterval: time.Millisecond,
	}, io.Discard, io.Discard, submitter, source)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("runWatcher() error = %v, want nil or context canceled", err)
	}
	if len(submitter.requests) != 1 {
		t.Fatalf("submitted %d requests, want new target only", len(submitter.requests))
	}
	if submitter.requests[0].payload.Tabs[0].Panes[0].Command[7] != "new-page" {
		t.Fatalf("payload = %#v, want target-id new-page", submitter.requests[0].payload)
	}
}

type fakeTargetSource struct {
	batches    [][]PageTarget
	calls      int
	afterBatch func(int)
}

func (f *fakeTargetSource) Targets(context.Context) ([]PageTarget, error) {
	idx := f.calls
	if idx >= len(f.batches) {
		idx = len(f.batches) - 1
	}
	f.calls++
	if f.afterBatch != nil {
		f.afterBatch(idx)
	}
	return f.batches[idx], nil
}

type submittedRequest struct {
	requestID string
	payload   transport.ExecutionPlanPayload
}

type fakeSubmitter struct {
	requests []submittedRequest
	err      error
}

func (f *fakeSubmitter) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	f.requests = append(f.requests, submittedRequest{requestID: requestID, payload: payload})
	if f.err != nil {
		return transport.ExecutionPlanResponse{}, f.err
	}
	return transport.ExecutionPlanResponse{RequestID: requestID, Session: payload.Session, Layout: payload.Layout}, nil
}
