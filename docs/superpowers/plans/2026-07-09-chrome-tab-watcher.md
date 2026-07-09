# Chrome Tab Watcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `zellij-agent chrome` start a Chrome tab watcher that creates a daemon-managed `tab-network` pane for each Chrome tab opened after watcher startup.

**Architecture:** Add a new `tab-watcher` role under `cmd/agent-role/tabwatcher`. The role launches or attaches to Chrome, snapshots startup page targets as baseline, polls for new page targets, and submits one-pane execution plans through `transport.Client`. Update `internal/chrome` and `internal/cli/chrome` so the default `zellij-agent chrome` plan starts the watcher while `--no-watch` preserves the current direct `tab-network` path.

**Tech Stack:** Go standard `flag`, `context`, `os/exec`, `net/http`; existing `chromedp`; existing `internal/transport`; existing role catalog and CLI dispatch patterns.

## Global Constraints

- The watcher must not call Zellij directly. All pane creation goes through daemon transport execution plans.
- Existing page targets at watcher startup are baseline and must not create panes.
- Only Chrome targets with `Type == "page"` create panes.
- A target creates at most one pane per watcher process.
- Generated tracker command must include `role tab-network --port <port> --no-launch --target-id <target-id>`.
- `zellij-agent chrome --no-watch` must preserve the current one-pane `tab-network` behavior.
- After rebuilding `bin/zellij-agent`, copy it to `~/.config/custom-cli`.
- Use TDD: write failing tests first, run them red, implement, then run green.

---

## File Structure

- Create `cmd/agent-role/tabwatcher/tabwatcher.go`: role implementation, option parsing, Chrome launch/attach helpers, target polling, plan submission.
- Create `cmd/agent-role/tabwatcher/tabwatcher_test.go`: focused unit tests using injected target sources and fake submitters.
- Modify `internal/roles/roles.go`: add role catalog metadata for `tab-watcher`.
- Modify `internal/roles/roles_test.go`: assert role exists and options are documented.
- Modify `internal/cli/role/role.go`: dispatch `tab-watcher`.
- Modify `internal/cli/role/role_test.go`: add observable validation dispatch test.
- Modify `internal/chrome/chrome.go`: support watcher plans by default and direct `tab-network` compatibility plans.
- Modify `internal/chrome/chrome_test.go`: update default expectations and add compatibility tests.
- Modify `internal/cli/chrome/chrome.go`: add `--no-watch`, pass socket/cwd/session/role binary/chrome args to plan builder.
- Modify `internal/cli/chrome/chrome_test.go`: update dry-run and submit tests for watcher default; add `--no-watch` test.
- Modify `cmd/zellij-agent/main_test.go`: update top-level chrome dry-run expectation to `tab-watcher`.
- Modify `/Users/in05908_mac/.config/pi/docs/agent-roles.md`: document `tab-watcher`.

---

### Task 1: Add `tab-watcher` Core Types, Option Parsing, And Plan Builder

**Files:**
- Create: `cmd/agent-role/tabwatcher/tabwatcher.go`
- Create: `cmd/agent-role/tabwatcher/tabwatcher_test.go`

**Interfaces:**
- Produces: `func Run(args []string) int`
- Produces: `func parseOptions(args []string) (watcherConfig, error)`
- Produces: `func buildTargetPlan(cfg watcherConfig, target PageTarget) (string, transport.ExecutionPlanPayload)`
- Produces: `type PageTarget struct { ID string; Type string; Title string; URL string }`
- Produces: `func shortTargetID(id string) string`

- [ ] **Step 1: Write failing tests for defaults, custom options, and generated target plans**

Add `cmd/agent-role/tabwatcher/tabwatcher_test.go`:

```go
package tabwatcher

import (
	"reflect"
	"testing"
	"time"
)

func TestParseOptionsDefaults(t *testing.T) {
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

func TestParseOptionsAcceptsCustomValues(t *testing.T) {
	opts, err := parseOptions([]string{
		"--port", "9333",
		"--socket", "/tmp/custom.sock",
		"--cwd", "/repo",
		"--session", "chrome-debug",
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

func TestBuildTargetPlanCreatesTabNetworkPaneForTarget(t *testing.T) {
	cfg := watcherConfig{
		Port:       9333,
		CWD:        "/repo",
		Session:    "chrome-tabs",
		RoleBin:    "/tmp/bin/zellij-agent",
		SocketPath: "/tmp/agentd.sock",
	}

	requestID, payload := buildTargetPlan(cfg, PageTarget{ID: "ABCDEF1234567890", Type: "page", URL: "https://example.com"})

	if requestID != "req_chrome-tab-network-ABCDEF123456" {
		t.Fatalf("requestID = %q, want target-specific request id", requestID)
	}
	if payload.Session != "chrome-tabs" || payload.Layout != "single-tab" || len(payload.Tabs) != 1 {
		t.Fatalf("payload = %#v, want one-tab chrome-tabs plan", payload)
	}
	if payload.Tabs[0].Name != "chrome:ABCDEF123456" {
		t.Fatalf("tab name = %q, want chrome:ABCDEF123456", payload.Tabs[0].Name)
	}
	pane := payload.Tabs[0].Panes[0]
	if pane.ID != "chrome-tab-network-ABCDEF123456" || pane.Role != "tab-network" || pane.CWD != "/repo" {
		t.Fatalf("pane = %#v, want deterministic tab-network pane in cwd", pane)
	}
	wantCommand := []string{"/tmp/bin/zellij-agent", "role", "tab-network", "--port", "9333", "--no-launch", "--target-id", "ABCDEF1234567890"}
	if !reflect.DeepEqual(pane.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", pane.Command, wantCommand)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```sh
go test ./cmd/agent-role/tabwatcher
```

Expected: FAIL because package/functions do not exist.

- [ ] **Step 3: Implement minimal core**

Create `cmd/agent-role/tabwatcher/tabwatcher.go` with:

```go
package tabwatcher

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/transport"
)

const defaultUserDataDir = "/tmp/chrome-debug-network-tracker"

type PageTarget struct {
	ID    string
	Type  string
	Title string
	URL   string
}

type watcherConfig struct {
	Port         int
	SocketPath   string
	CWD          string
	Session      string
	RoleBin      string
	ChromePath   string
	UserDataDir  string
	LaunchChrome bool
	PollInterval time.Duration
}

type planSubmitter interface {
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
}

func Run(args []string) int {
	return runWithIO(args, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptionsWithOutput(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	_ = opts
	fmt.Fprintln(stdout, "tab-watcher runtime not started")
	return 0
}

func parseOptions(args []string) (watcherConfig, error) {
	return parseOptionsWithOutput(args, io.Discard)
}

func parseOptionsWithOutput(args []string, output io.Writer) (watcherConfig, error) {
	opts := watcherConfig{
		Port:         9222,
		SocketPath:   cli.DefaultSocketPath,
		Session:      "chrome-tabs",
		RoleBin:      "zellij-agent",
		UserDataDir:  defaultUserDataDir,
		LaunchChrome: true,
		PollInterval: 500 * time.Millisecond,
	}
	fs := flag.NewFlagSet("tab-watcher", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.IntVar(&opts.Port, "port", opts.Port, "Chrome remote debugging port")
	fs.StringVar(&opts.SocketPath, "socket", opts.SocketPath, "agentd Unix socket path")
	fs.StringVar(&opts.CWD, "cwd", "", "working directory for generated tab-network panes")
	fs.StringVar(&opts.Session, "session", opts.Session, "execution session/task id for generated tab panes")
	fs.StringVar(&opts.RoleBin, "role-bin", opts.RoleBin, "executable used to run zellij-agent roles")
	fs.StringVar(&opts.ChromePath, "chrome-path", "", "Chrome executable path")
	fs.StringVar(&opts.UserDataDir, "user-data-dir", opts.UserDataDir, "Chrome user data directory used when launching Chrome")
	noLaunch := fs.Bool("no-launch", false, "do not launch Chrome; attach to an already running debug port")
	fs.DurationVar(&opts.PollInterval, "poll-interval", opts.PollInterval, "Chrome target polling interval")
	if err := fs.Parse(args); err != nil {
		return watcherConfig{}, err
	}
	if fs.NArg() != 0 {
		return watcherConfig{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return watcherConfig{}, fmt.Errorf("--port must be between 1 and 65535")
	}
	if opts.PollInterval <= 0 {
		return watcherConfig{}, fmt.Errorf("--poll-interval must be positive")
	}
	opts.LaunchChrome = !*noLaunch
	if opts.CWD != "" {
		abs, err := filepath.Abs(opts.CWD)
		if err != nil {
			return watcherConfig{}, err
		}
		opts.CWD = abs
	}
	return opts, nil
}

func buildTargetPlan(cfg watcherConfig, target PageTarget) (string, transport.ExecutionPlanPayload) {
	shortID := shortTargetID(target.ID)
	paneID := "chrome-tab-network-" + shortID
	command := []string{
		cfg.RoleBin,
		"role",
		"tab-network",
		"--port",
		strconv.Itoa(cfg.Port),
		"--no-launch",
		"--target-id",
		target.ID,
	}
	payload := transport.ExecutionPlanPayload{
		Session: cfg.Session,
		Layout:  "single-tab",
		Tabs: []transport.ExecutionPlanTab{{
			Name: "chrome:" + shortID,
			Panes: []transport.ExecutionPlanPane{{
				ID:      paneID,
				Role:    "tab-network",
				Command: command,
				CWD:     cfg.CWD,
			}},
		}},
	}
	return "req_" + paneID, payload
}

func shortTargetID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```sh
go test ./cmd/agent-role/tabwatcher
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add cmd/agent-role/tabwatcher
git commit -m "feat: add tab watcher plan builder"
```

---

### Task 2: Add Baseline Filtering And Poll Processing

**Files:**
- Modify: `cmd/agent-role/tabwatcher/tabwatcher.go`
- Modify: `cmd/agent-role/tabwatcher/tabwatcher_test.go`

**Interfaces:**
- Consumes: `watcherConfig`, `PageTarget`, `buildTargetPlan`
- Produces: `type targetTracker struct`
- Produces: `func newTargetTracker(cfg watcherConfig, submitter planSubmitter, stdout, stderr io.Writer) *targetTracker`
- Produces: `func (t *targetTracker) MarkBaseline(targets []PageTarget)`
- Produces: `func (t *targetTracker) ProcessTargets(ctx context.Context, targets []PageTarget)`

- [ ] **Step 1: Write failing tests for baseline, new targets, non-page filtering, and dedupe**

Append to `cmd/agent-role/tabwatcher/tabwatcher_test.go`:

```go
import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"zellij-with-codeagent/internal/transport"
)

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
```

If the import block cannot be duplicated, merge these imports into the existing block.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```sh
go test ./cmd/agent-role/tabwatcher
```

Expected: FAIL because `newTargetTracker`, `MarkBaseline`, and `ProcessTargets` are undefined.

- [ ] **Step 3: Implement target tracker**

Add to `cmd/agent-role/tabwatcher/tabwatcher.go`:

```go
type targetTracker struct {
	cfg       watcherConfig
	submitter planSubmitter
	stdout    io.Writer
	stderr    io.Writer
	seen      map[string]struct{}
}

func newTargetTracker(cfg watcherConfig, submitter planSubmitter, stdout, stderr io.Writer) *targetTracker {
	return &targetTracker{
		cfg:       cfg,
		submitter: submitter,
		stdout:    stdout,
		stderr:    stderr,
		seen:      map[string]struct{}{},
	}
}

func (t *targetTracker) MarkBaseline(targets []PageTarget) {
	count := 0
	for _, target := range targets {
		if target.Type != "page" || strings.TrimSpace(target.ID) == "" {
			continue
		}
		t.seen[target.ID] = struct{}{}
		count++
	}
	fmt.Fprintf(t.stdout, "baseline page-targets=%d\n", count)
}

func (t *targetTracker) ProcessTargets(ctx context.Context, targets []PageTarget) {
	for _, target := range targets {
		if target.Type != "page" || strings.TrimSpace(target.ID) == "" {
			continue
		}
		if _, ok := t.seen[target.ID]; ok {
			continue
		}
		t.seen[target.ID] = struct{}{}
		requestID, payload := buildTargetPlan(t.cfg, target)
		if _, err := t.submitter.SubmitExecutionPlan(ctx, requestID, payload); err != nil {
			fmt.Fprintf(t.stderr, "submit target=%s failed: %v\n", target.ID, err)
			continue
		}
		fmt.Fprintf(t.stdout, "submitted target=%s request=%s\n", target.ID, requestID)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```sh
gofmt -w cmd/agent-role/tabwatcher/tabwatcher.go cmd/agent-role/tabwatcher/tabwatcher_test.go
go test ./cmd/agent-role/tabwatcher
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add cmd/agent-role/tabwatcher
git commit -m "feat: track new chrome page targets"
```

---

### Task 3: Add Chrome Launch, Attach, And Watcher Runtime Loop

**Files:**
- Modify: `cmd/agent-role/tabwatcher/tabwatcher.go`
- Modify: `cmd/agent-role/tabwatcher/tabwatcher_test.go`

**Interfaces:**
- Consumes: `targetTracker`
- Produces: `func runWatcher(ctx context.Context, cfg watcherConfig, stdout, stderr io.Writer, submitter planSubmitter, source targetSource) error`
- Produces: `type targetSource interface { Targets(context.Context) ([]PageTarget, error) }`
- Produces: `func chromeArgs(port int, userDataDir string) []string`
- Produces: `func resolveChromePath(chromePath string) (string, error)`

- [ ] **Step 1: Write failing tests for runtime loop and Chrome args**

Append to `cmd/agent-role/tabwatcher/tabwatcher_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```sh
go test ./cmd/agent-role/tabwatcher
```

Expected: FAIL because runtime helpers are missing.

- [ ] **Step 3: Implement runtime loop and helpers**

Add imports to `tabwatcher.go`:

```go
import (
	"net/http"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/chromedp/chromedp"
)
```

Add:

```go
type targetSource interface {
	Targets(context.Context) ([]PageTarget, error)
}

type chromedpTargetSource struct {
	ctx context.Context
}

func (s chromedpTargetSource) Targets(ctx context.Context) ([]PageTarget, error) {
	_ = ctx
	infos, err := chromedp.Targets(s.ctx)
	if err != nil {
		return nil, err
	}
	targets := make([]PageTarget, 0, len(infos))
	for _, info := range infos {
		targets = append(targets, PageTarget{
			ID:    string(info.TargetID),
			Type:  info.Type,
			Title: info.Title,
			URL:   info.URL,
		})
	}
	return targets, nil
}

func runWatcher(ctx context.Context, cfg watcherConfig, stdout, stderr io.Writer, submitter planSubmitter, source targetSource) error {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	fmt.Fprintf(stdout, "tab-watcher port=%d socket=%s cwd=%s\n", cfg.Port, cfg.SocketPath, cfg.CWD)
	initial, err := source.Targets(ctx)
	if err != nil {
		return err
	}
	tracker := newTargetTracker(cfg, submitter, stdout, stderr)
	tracker.MarkBaseline(initial)
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			targets, err := source.Targets(ctx)
			if err != nil {
				if ctx.Err() == nil {
					fmt.Fprintf(stderr, "target poll failed: %v\n", err)
				}
				continue
			}
			tracker.ProcessTargets(ctx, targets)
		}
	}
}

func chromeArgs(port int, userDataDir string) []string {
	return []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	}
}

func resolveChromePath(chromePath string) (string, error) {
	if chromePath != "" {
		return chromePath, nil
	}
	if envPath := os.Getenv("CHROME_PATH"); envPath != "" {
		return envPath, nil
	}
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		"google-chrome",
		"chromium",
		"chromium-browser",
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, "/") {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("Chrome executable not found; pass --chrome-path or set CHROME_PATH")
}

func launchChrome(ctx context.Context, chromePath string, port int, userDataDir string) (*exec.Cmd, error) {
	path, err := resolveChromePath(chromePath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(userDataDir, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, chromeArgs(port, userDataDir)...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd, nil
}

func waitForDebugPort(ctx context.Context, port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	client := http.Client{Timeout: 500 * time.Millisecond}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Chrome debug port %d did not become ready: %w", port, ctx.Err())
		case <-ticker.C:
		}
	}
}
```

Replace `runWithIO` with real startup:

```go
func runWithIO(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptionsWithOutput(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if opts.CWD == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "Error: resolve cwd: %v\n", err)
			return 1
		}
		opts.CWD = cwd
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.LaunchChrome {
		if _, err := launchChrome(ctx, opts.ChromePath, opts.Port, opts.UserDataDir); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
	}
	if err := waitForDebugPort(ctx, opts.Port, 10*time.Second); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	allocatorURL := fmt.Sprintf("http://127.0.0.1:%d", opts.Port)
	allocatorCtx, cancelAllocator := chromedp.NewRemoteAllocator(ctx, allocatorURL)
	defer cancelAllocator()
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	defer cancelBrowser()
	client := transport.NewClient(transport.ClientOptions{
		SocketPath: opts.SocketPath,
		Timeout:    10 * time.Second,
	})
	err = runWatcher(ctx, opts, stdout, stderr, client, chromedpTargetSource{ctx: browserCtx})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```sh
gofmt -w cmd/agent-role/tabwatcher/tabwatcher.go cmd/agent-role/tabwatcher/tabwatcher_test.go
go test ./cmd/agent-role/tabwatcher
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add cmd/agent-role/tabwatcher
git commit -m "feat: run chrome tab watcher"
```

---

### Task 4: Register And Dispatch The `tab-watcher` Role

**Files:**
- Modify: `internal/roles/roles.go`
- Modify: `internal/roles/roles_test.go`
- Modify: `internal/cli/role/role.go`
- Modify: `internal/cli/role/role_test.go`
- Modify: `/Users/in05908_mac/.config/pi/docs/agent-roles.md`

**Interfaces:**
- Consumes: `cmd/agent-role/tabwatcher.Run(args []string) int`
- Produces: `roles.RoleTabWatcher`

- [ ] **Step 1: Write failing catalog and dispatch tests**

Modify `internal/roles/roles_test.go`:

```go
func TestLookupTabWatcher(t *testing.T) {
	spec, ok := Lookup(RoleTabWatcher)
	if !ok {
		t.Fatal("Lookup(RoleTabWatcher) ok = false, want true")
	}
	if spec.Name != "tab-watcher" {
		t.Fatalf("name = %q, want tab-watcher", spec.Name)
	}
	if spec.Usage != "tab-watcher [options]" {
		t.Fatalf("usage = %q, want tab-watcher [options]", spec.Usage)
	}
	want := []string{"--port", "--socket", "--cwd", "--session", "--role-bin", "--chrome-path", "--user-data-dir", "--no-launch", "--poll-interval"}
	for _, name := range want {
		if !hasArgument(spec.Arguments, name) {
			t.Fatalf("arguments = %#v, missing %s", spec.Arguments, name)
		}
	}
}

func hasArgument(args []ArgumentSpec, name string) bool {
	for _, arg := range args {
		if arg.Name == name {
			return true
		}
	}
	return false
}
```

Modify `internal/cli/role/role_test.go`:

```go
func TestRunDispatchesTabWatcherValidation(t *testing.T) {
	if code := Run([]string{"tab-watcher", "--port", "0"}); code == 0 {
		t.Fatalf("Run(tab-watcher --port 0) = %d, want non-zero", code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```sh
go test ./internal/roles ./internal/cli/role
```

Expected: FAIL because `RoleTabWatcher` and dispatch are missing.

- [ ] **Step 3: Register role metadata**

Modify `internal/roles/roles.go`:

```go
const (
	RoleCoder             = "coder"
	RoleEditor            = "editor"
	RoleLSP               = "lsp"
	RoleNetworkTracker    = "network-tracker"
	RoleConsoleTracker    = "console-tracker"
	RoleTabNetwork        = "tab-network"
	RoleTabWatcher        = "tab-watcher"
	RoleCodingAgent       = "coding-agent"
	RoleDebateCoordinator = "debate-coordinator"
)
```

Add this `RoleSpec` after `RoleTabNetwork`:

```go
{
	Name:        RoleTabWatcher,
	Usage:       "tab-watcher [options]",
	Description: "Watches Chrome tabs and starts tab-network panes for newly opened tabs.",
	Arguments: []ArgumentSpec{
		{Name: "--port", Required: false, Description: "Chrome remote debugging port. Defaults to 9222."},
		{Name: "--socket", Required: false, Description: "agentd Unix socket path."},
		{Name: "--cwd", Required: false, Description: "Working directory for generated tab-network panes."},
		{Name: "--session", Required: false, Description: "Execution session/task id for generated tab panes."},
		{Name: "--role-bin", Required: false, Description: "Executable used to run zellij-agent roles."},
		{Name: "--chrome-path", Required: false, Description: "Chrome executable path."},
		{Name: "--user-data-dir", Required: false, Description: "Chrome profile directory used when launching Chrome."},
		{Name: "--no-launch", Required: false, Description: "Attach to an already running Chrome debug port."},
		{Name: "--poll-interval", Required: false, Description: "Chrome target polling interval."},
	},
},
```

- [ ] **Step 4: Wire role dispatch**

Modify `internal/cli/role/role.go` imports:

```go
	"zellij-with-codeagent/cmd/agent-role/tabwatcher"
```

Add dispatch case:

```go
	case roles.RoleTabWatcher:
		return tabwatcher.Run(args[1:])
```

- [ ] **Step 5: Update external role docs**

Open `/Users/in05908_mac/.config/pi/docs/agent-roles.md`. Add or update an entry:

```markdown
### tab-watcher

Usage: `zellij-agent role tab-watcher [options]`

Purpose: Watches a Chrome remote debugging endpoint and asks the daemon to create `tab-network` panes for Chrome page targets opened after watcher startup.

Options: `--port`, `--socket`, `--cwd`, `--session`, `--role-bin`, `--chrome-path`, `--user-data-dir`, `--no-launch`, `--poll-interval`.

Runtime requirements: Chrome or Chromium must be available, or `--chrome-path`/`CHROME_PATH` must point to an executable. The agent daemon must be reachable at `--socket`.
```

- [ ] **Step 6: Run tests to verify they pass**

Run:

```sh
gofmt -w internal/roles/roles.go internal/roles/roles_test.go internal/cli/role/role.go internal/cli/role/role_test.go
go test ./internal/roles ./internal/cli/role
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/roles/roles.go internal/roles/roles_test.go internal/cli/role/role.go internal/cli/role/role_test.go /Users/in05908_mac/.config/pi/docs/agent-roles.md
git commit -m "feat: register tab watcher role"
```

---

### Task 5: Update Chrome Plan Builder For Watcher Default And `--no-watch`

**Files:**
- Modify: `internal/chrome/chrome.go`
- Modify: `internal/chrome/chrome_test.go`

**Interfaces:**
- Produces: `PlanRequest.NoWatch bool`
- Produces: `PlanRequest.SocketPath string`
- Existing `BuildPlan(req PlanRequest) (transport.ExecutionPlanPayload, error)` remains the public entrypoint.

- [ ] **Step 1: Write failing tests for watcher default and compatibility**

Replace `TestBuildPlanCreatesChromeTabNetworkPane` in `internal/chrome/chrome_test.go` with:

```go
func TestBuildPlanCreatesChromeTabWatcherPaneByDefault(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 34, 56, 123456789, time.UTC)

	payload, err := BuildPlan(PlanRequest{
		CWD:         "/repo",
		SocketPath:  "/tmp/agentd.sock",
		Session:     "chrome-debug",
		RoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	if payload.Session != "chrome-debug" || payload.Layout != "single-tab" {
		t.Fatalf("payload session/layout = %q/%q, want chrome-debug/single-tab", payload.Session, payload.Layout)
	}
	pane := payload.Tabs[0].Panes[0]
	if pane.ID != "chrome-tab-watcher-20260708-123456-123456789" {
		t.Fatalf("pane.ID = %q, want timestamped chrome tab-watcher id", pane.ID)
	}
	if pane.Role != "tab-watcher" || pane.CWD != "/repo" {
		t.Fatalf("pane role/cwd = %q/%q, want tab-watcher /repo", pane.Role, pane.CWD)
	}
	wantCommand := []string{
		"/tmp/bin/zellij-agent", "role", "tab-watcher",
		"--socket", "/tmp/agentd.sock",
		"--cwd", "/repo",
		"--session", "chrome-debug",
		"--role-bin", "/tmp/bin/zellij-agent",
	}
	if !reflect.DeepEqual(pane.Command, wantCommand) {
		t.Fatalf("pane.Command = %#v, want %#v", pane.Command, wantCommand)
	}
}
```

Add:

```go
func TestBuildPlanNoWatchCreatesChromeTabNetworkPane(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 34, 56, 123456789, time.UTC)

	payload, err := BuildPlan(PlanRequest{
		CWD:            "/repo",
		Session:        "chrome-debug",
		RoleCommand:    []string{"/tmp/bin/zellij-agent", "role"},
		TabNetworkArgs: []string{"--port", "9333", "--no-launch"},
		NoWatch:        true,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	pane := payload.Tabs[0].Panes[0]
	if pane.ID != "chrome-tab-network-20260708-123456-123456789" || pane.Role != "tab-network" {
		t.Fatalf("pane = %#v, want timestamped tab-network pane", pane)
	}
	wantCommand := []string{"/tmp/bin/zellij-agent", "role", "tab-network", "--port", "9333", "--no-launch"}
	if !reflect.DeepEqual(pane.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", pane.Command, wantCommand)
	}
}

func TestBuildPlanPassesWatcherChromeArgs(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 34, 56, 123456789, time.UTC)

	payload, err := BuildPlan(PlanRequest{
		CWD:            "/repo",
		SocketPath:     "/tmp/agentd.sock",
		Session:        "chrome-debug",
		RoleCommand:    []string{"zellij-agent", "role"},
		TabNetworkArgs: []string{"--port", "9333", "--no-launch"},
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	got := payload.Tabs[0].Panes[0].Command
	want := []string{
		"zellij-agent", "role", "tab-watcher",
		"--socket", "/tmp/agentd.sock",
		"--cwd", "/repo",
		"--session", "chrome-debug",
		"--role-bin", "zellij-agent",
		"--port", "9333",
		"--no-launch",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```sh
go test ./internal/chrome
```

Expected: FAIL because default still builds `tab-network` and fields are missing.

- [ ] **Step 3: Implement watcher/default plan behavior**

Modify `internal/chrome/chrome.go`:

```go
type PlanRequest struct {
	CWD            string
	Session        string
	SocketPath     string
	RoleCommand    []string
	TabNetworkArgs []string
	NoWatch        bool
	Now            func() time.Time
}
```

In `BuildPlan`, after resolving `roleCommand`, choose:

```go
role := "tab-watcher"
idPrefix := "chrome-tab-watcher"
command := append(append([]string{}, roleCommand...), "tab-watcher")
if req.SocketPath != "" {
	command = append(command, "--socket", req.SocketPath)
}
command = append(command,
	"--cwd", cwd,
	"--session", session,
	"--role-bin", roleCommand[0],
)
command = append(command, req.TabNetworkArgs...)
if req.NoWatch {
	role = "tab-network"
	idPrefix = "chrome-tab-network"
	command = append(append([]string{}, roleCommand...), "tab-network")
	command = append(command, req.TabNetworkArgs...)
}
```

Replace `paneID(now())` with `paneID(idPrefix, now())`, and change `paneID`:

```go
func paneID(prefix string, t time.Time) string {
	t = t.UTC()
	return fmt.Sprintf("%s-%s-%09d", prefix, t.Format("20060102-150405"), t.Nanosecond())
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```sh
gofmt -w internal/chrome/chrome.go internal/chrome/chrome_test.go
go test ./internal/chrome
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/chrome/chrome.go internal/chrome/chrome_test.go
git commit -m "feat: build chrome watcher plans"
```

---

### Task 6: Update `zellij-agent chrome` CLI And Top-Level Tests

**Files:**
- Modify: `internal/cli/chrome/chrome.go`
- Modify: `internal/cli/chrome/chrome_test.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Consumes: `chrome.PlanRequest.NoWatch`, `SocketPath`
- Produces: `zellij-agent chrome --no-watch`

- [ ] **Step 1: Write failing CLI tests**

Modify `internal/cli/chrome/chrome_test.go` default dry-run expectations. Add `reflect` to the import block if it is not already present:

```go
if payload.Session != "chrome-debug" || payload.Layout != "single-tab" || payload.Tabs[0].Name != "chrome" {
	t.Fatalf("payload = %#v, want chrome-debug single chrome tab", payload)
}
gotCommand := payload.Tabs[0].Panes[0].Command
wantCommand := []string{
	"/tmp/bin/zellij-agent", "role", "tab-watcher",
	"--socket", "/tmp/agentd.sock",
	"--cwd", cwd,
	"--session", "chrome-debug",
	"--role-bin", "/tmp/bin/zellij-agent",
	"--port", "9333",
	"--no-launch",
}
if strings.Join(gotCommand, "\x00") != strings.Join(wantCommand, "\x00") {
	t.Fatalf("command = %#v, want %#v", gotCommand, wantCommand)
}
```

Add:

```go
func TestRunNoWatchDryRunPrintsTabNetworkPlan(t *testing.T) {
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--cwd", cwd,
		"--session", "chrome-debug",
		"--dry-run",
		"--no-watch",
		"--", "--port", "9333", "--no-launch",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
		Now:                fixedNow,
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	pane := payload.Tabs[0].Panes[0]
	wantCommand := []string{"/tmp/bin/zellij-agent", "role", "tab-network", "--port", "9333", "--no-launch"}
	if !reflect.DeepEqual(pane.Command, wantCommand) || pane.Role != "tab-network" {
		t.Fatalf("pane = %#v, want no-watch tab-network command", pane)
	}
}
```

Update `cmd/zellij-agent/main_test.go` chrome dry-run assertion to expect `tab-watcher` and port passthrough:

```go
if pane.Role != "tab-watcher" || len(pane.Command) < 4 || pane.Command[1] != "role" || pane.Command[2] != "tab-watcher" {
	t.Fatalf("pane = %#v, want tab-watcher command", pane)
}
if !containsAdjacent(pane.Command, "--port", "9333") {
	t.Fatalf("command = %#v, want passthrough port", pane.Command)
}
```

Add helper in `cmd/zellij-agent/main_test.go`:

```go
func containsAdjacent(values []string, key, value string) bool {
	for i := 0; i+1 < len(values); i++ {
		if values[i] == key && values[i+1] == value {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```sh
go test ./internal/cli/chrome ./cmd/zellij-agent
```

Expected: FAIL because CLI does not pass `SocketPath` or `NoWatch`.

- [ ] **Step 3: Implement CLI flags and plan request wiring**

Modify `internal/cli/chrome/chrome.go`:

```go
	noWatch := fs.Bool("no-watch", false, "start one tab-network pane instead of watching new Chrome tabs")
```

Pass fields to `chrome.BuildPlan`:

```go
	payload, err := chrome.BuildPlan(chrome.PlanRequest{
		CWD:            cwd,
		Session:        *session,
		SocketPath:     *socketPath,
		RoleCommand:    cfg.DefaultRoleCommand,
		TabNetworkArgs: fs.Args(),
		NoWatch:        *noWatch,
		Now:            cfg.Now,
	})
```

Update usage:

```go
fmt.Fprintln(w, "  --no-watch")
fmt.Fprintln(w, "    \tstart one tab-network pane instead of watching new Chrome tabs")
fmt.Fprintln(w, "  zellij-agent chrome -- --port 9333 --no-launch")
fmt.Fprintln(w, "  zellij-agent chrome --no-watch -- --port 9333 --no-launch")
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```sh
gofmt -w internal/cli/chrome/chrome.go internal/cli/chrome/chrome_test.go cmd/zellij-agent/main_test.go
go test ./internal/cli/chrome ./cmd/zellij-agent
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/cli/chrome/chrome.go internal/cli/chrome/chrome_test.go cmd/zellij-agent/main_test.go
git commit -m "feat: default chrome command to tab watcher"
```

---

### Task 7: Final Verification, Build, Install, And Smoke Check

**Files:**
- No source edits unless verification exposes a defect.

**Interfaces:**
- Consumes: all previous tasks.
- Produces: rebuilt `bin/zellij-agent` and copied `~/.config/custom-cli`.

- [ ] **Step 1: Run focused tests**

Run:

```sh
go test ./cmd/agent-role/tabwatcher ./internal/chrome ./internal/cli/chrome ./internal/roles ./internal/cli/role ./cmd/zellij-agent
```

Expected: PASS.

- [ ] **Step 2: Run full test suite**

Run:

```sh
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Build unified binary**

Run:

```sh
go build -o bin/zellij-agent ./cmd/zellij-agent
```

Expected: exit code 0.

- [ ] **Step 4: Register custom CLI binary**

Run:

```sh
cp bin/zellij-agent ~/.config/custom-cli
```

Expected: exit code 0.

- [ ] **Step 5: Verify dry-run output**

Run:

```sh
./bin/zellij-agent chrome --cwd /Users/in05908_mac/zellij-with-codeagent --session chrome-watch --dry-run -- --port 49335 --no-launch
```

Expected: JSON `execution_plan` whose pane role is `tab-watcher` and command contains `--port 49335 --no-launch`.

- [ ] **Step 6: Verify compatibility dry-run**

Run:

```sh
./bin/zellij-agent chrome --cwd /Users/in05908_mac/zellij-with-codeagent --session chrome-direct --dry-run --no-watch -- --port 49335 --no-launch
```

Expected: JSON `execution_plan` whose pane role is `tab-network` and command contains `--target-id` only if passed by the caller.

- [ ] **Step 7: Manual smoke if running inside Zellij**

Run:

```sh
./bin/zellij-agent chrome --session chrome-watch-smoke -- --port 49335 --user-data-dir /tmp/zellij-agent-chrome-watch-49335
```

Then open a new tab in that Chrome instance. Verify:

```sh
./bin/zellij-agent ctl status
./bin/zellij-agent ctl events
```

Expected: watcher pane exists, and a new `tab-network` pane appears after a new Chrome tab is opened.

- [ ] **Step 8: Commit any verification fixes**

If no fixes were needed, do not create an empty commit. If fixes were needed:

```sh
git add <changed-files>
git commit -m "fix: verify chrome tab watcher"
```
