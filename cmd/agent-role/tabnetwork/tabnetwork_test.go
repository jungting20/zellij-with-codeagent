package tabnetwork

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/transport"
)

func TestParseOptionsDefaults(t *testing.T) {
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions(nil) error = %v", err)
	}

	if opts.Port != 9222 {
		t.Fatalf("Port = %d, want 9222", opts.Port)
	}
	if opts.UserDataDir != defaultUserDataDir {
		t.Fatalf("UserDataDir = %q, want %q", opts.UserDataDir, defaultUserDataDir)
	}
	if opts.LaunchChrome != true {
		t.Fatalf("LaunchChrome = %v, want true", opts.LaunchChrome)
	}
	if opts.MaxRows != 500 {
		t.Fatalf("MaxRows = %d, want 500", opts.MaxRows)
	}
}

func TestParseOptionsAcceptsFiltersAndNoLaunch(t *testing.T) {
	opts, err := parseOptions([]string{
		"--port", "9333",
		"--no-launch",
		"--target-id", "target-123",
		"--filter-url", "/api/",
		"--method", "post",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if opts.Port != 9333 || opts.LaunchChrome || opts.TargetID != "target-123" || opts.Filter.URLContains != "/api/" || opts.Filter.Method != "POST" {
		t.Fatalf("options = %#v, want parsed filters and no launch", opts)
	}
}

func TestParseOptionsAcceptsZellijSessionForSpawnOptions(t *testing.T) {
	opts, err := parseOptions([]string{
		"--port", "9333",
		"--socket", "/tmp/custom.sock",
		"--role-bin", "/tmp/bin/zellij-agent",
		"--session", "chrome-task",
		"--zellij-session", " physical-a ",
		"--spawn-on-new-tab",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}

	if opts.SocketPath != "/tmp/custom.sock" || opts.RoleBin != "/tmp/bin/zellij-agent" || opts.Session != "chrome-task" || opts.ZellijSession != "physical-a" || !opts.SpawnOnNewTab {
		t.Fatalf("options = %#v, want spawn options", opts)
	}

	opts, err = parseOptions([]string{"--spawn-on-new-tab", "--no-spawn-on-new-tab"})
	if err != nil {
		t.Fatalf("parseOptions() second error = %v", err)
	}
	if opts.SpawnOnNewTab {
		t.Fatalf("SpawnOnNewTab = true, want false after --no-spawn-on-new-tab")
	}

	t.Setenv("ZELLIJ_SESSION_NAME", "")
	opts, err = parseOptions([]string{"--list", "--spawn-on-new-tab"})
	if err != nil {
		t.Fatalf("parseOptions() list mode error = %v", err)
	}
}

func TestParseOptionsRejectsMissingZellijSessionWhenSpawning(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	_, err := parseOptions([]string{"--spawn-on-new-tab"})
	if !errors.Is(err, cli.ErrZellijSessionRequired) {
		t.Fatalf("parseOptions() error = %v, want %v", err, cli.ErrZellijSessionRequired)
	}
}

func TestBuildChildPaneRequestPreservesZellijSessionInParentTab(t *testing.T) {
	tabID := 7
	cfg := trackerConfig{
		Port:          9333,
		RoleBin:       "/tmp/bin/zellij-agent",
		Session:       "chrome-task",
		UserDataDir:   defaultUserDataDir,
		MaxRows:       defaultMaxRows,
		ZellijSession: "physical-a",
	}

	req := buildChildPaneRequest(cfg, PageTarget{ID: "ABCDEF1234567890", Type: "page"}, tabID, "/repo")

	if req.ID != "chrome-tab-network-ABCDEF123456" || req.Role != "tab-network" || req.TaskID != "chrome-task" || req.CWD != "/repo" {
		t.Fatalf("request = %#v, want child tab-network pane request", req)
	}
	if req.ZellijSession != "physical-a" {
		t.Fatalf("ZellijSession = %q, want physical-a", req.ZellijSession)
	}
	if req.ZellijTabID == nil || *req.ZellijTabID != tabID {
		t.Fatalf("ZellijTabID = %v, want %d", req.ZellijTabID, tabID)
	}
	wantCommand := []string{"/tmp/bin/zellij-agent", "role", "tab-network", "--port", "9333", "--no-launch", "--target-id", "ABCDEF1234567890", "--no-spawn-on-new-tab", "--zellij-session", "physical-a"}
	if !reflect.DeepEqual(req.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", req.Command, wantCommand)
	}
}

func TestResolveOwnManagedPaneFindsZellijTab(t *testing.T) {
	client := &fakePaneClient{runtime: transport.InspectRuntimeResponse{
		Panes: []transport.Pane{
			{ID: "other", SessionID: "physical-a", ZellijPaneID: "terminal_1"},
			{ID: "parent", SessionID: "physical-a", ZellijPaneID: "terminal_42", ZellijTabID: intPtr(9), CWD: "/repo"},
		},
	}}

	pane, err := resolveOwnManagedPane(context.Background(), client, "physical-a", "terminal_42")
	if err != nil {
		t.Fatalf("resolveOwnManagedPane() error = %v", err)
	}
	if pane.ID != "parent" || pane.ZellijTabID == nil || *pane.ZellijTabID != 9 || pane.CWD != "/repo" {
		t.Fatalf("pane = %#v, want parent pane with tab metadata", pane)
	}
}

func TestResolveOwnManagedPaneUsesZellijSessionForCollidingPaneIDs(t *testing.T) {
	client := &fakePaneClient{runtime: transport.InspectRuntimeResponse{
		Panes: []transport.Pane{
			{ID: "wrong", SessionID: "physical-b", ZellijPaneID: "terminal_42", ZellijTabID: intPtr(7), CWD: "/wrong"},
			{ID: "parent", SessionID: "physical-a", ZellijPaneID: "terminal_42", ZellijTabID: intPtr(9), CWD: "/repo"},
		},
	}}

	pane, err := resolveOwnManagedPane(context.Background(), client, "physical-a", "terminal_42")
	if err != nil {
		t.Fatalf("resolveOwnManagedPane() error = %v", err)
	}
	if pane.ID != "parent" || pane.ZellijTabID == nil || *pane.ZellijTabID != 9 || pane.CWD != "/repo" {
		t.Fatalf("pane = %#v, want physical-a parent pane", pane)
	}
}

func TestResolveOwnManagedPaneNormalizesNumericZellijPaneID(t *testing.T) {
	client := &fakePaneClient{runtime: transport.InspectRuntimeResponse{
		Panes: []transport.Pane{{ID: "parent", SessionID: "physical-a", ZellijPaneID: "terminal_42", ZellijTabID: intPtr(9)}},
	}}

	pane, err := resolveOwnManagedPane(context.Background(), client, "physical-a", "42")
	if err != nil {
		t.Fatalf("resolveOwnManagedPane() error = %v", err)
	}
	if pane.ID != "parent" {
		t.Fatalf("pane.ID = %q, want parent", pane.ID)
	}
}

func TestResolveOwnManagedPaneRejectsMissingTabID(t *testing.T) {
	client := &fakePaneClient{runtime: transport.InspectRuntimeResponse{
		Panes: []transport.Pane{{ID: "parent", SessionID: "physical-a", ZellijPaneID: "terminal_42"}},
	}}

	_, err := resolveOwnManagedPane(context.Background(), client, "physical-a", "terminal_42")
	if err == nil || !strings.Contains(err.Error(), "missing zellij tab id") {
		t.Fatalf("resolveOwnManagedPane() error = %v, want missing tab id", err)
	}
}

func TestWaitForOwnManagedPaneRetriesUntilPaneIsRegistered(t *testing.T) {
	client := &fakePaneClient{runtimeResponses: []transport.InspectRuntimeResponse{
		{},
		{Panes: []transport.Pane{{ID: "parent", SessionID: "physical-a", ZellijPaneID: "terminal_42", ZellijTabID: intPtr(9)}}},
	}}

	pane, err := waitForOwnManagedPane(context.Background(), client, "physical-a", "42", time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("waitForOwnManagedPane() error = %v", err)
	}
	if pane.ID != "parent" || client.inspectCalls != 2 {
		t.Fatalf("pane = %#v inspectCalls = %d, want parent after retry", pane, client.inspectCalls)
	}
}

func TestTargetPaneSpawnerBaselinesAndCreatesOnlyNewPageTargets(t *testing.T) {
	client := &fakePaneClient{}
	spawner := newTargetPaneSpawner(trackerConfig{
		Port:    9333,
		RoleBin: "/tmp/bin/zellij-agent",
		Session: "chrome-task",
	}, client, 9, "/repo", io.Discard, io.Discard)

	spawner.MarkBaseline([]PageTarget{{ID: "existing", Type: "page"}})
	spawner.ProcessTargets(context.Background(), []PageTarget{
		{ID: "existing", Type: "page"},
		{ID: "worker", Type: "service_worker"},
		{ID: "new-target-123456", Type: "page"},
		{ID: "new-target-123456", Type: "page"},
	})

	if len(client.createRequests) != 1 {
		t.Fatalf("create requests = %#v, want exactly one child pane", client.createRequests)
	}
	req := client.createRequests[0]
	if req.ID != "chrome-tab-network-new-target-1" || req.ZellijTabID == nil || *req.ZellijTabID != 9 {
		t.Fatalf("request = %#v, want new target in parent tab", req)
	}
}

func TestTargetPaneSpawnerCleansUpPaneWhenTargetCloses(t *testing.T) {
	client := &fakePaneClient{}
	spawner := newTargetPaneSpawner(trackerConfig{
		Port:    9333,
		RoleBin: "/tmp/bin/zellij-agent",
		Session: "chrome-task",
	}, client, 9, "/repo", io.Discard, io.Discard)

	spawner.MarkBaseline([]PageTarget{{ID: "existing", Type: "page"}})
	spawner.ProcessTargets(context.Background(), []PageTarget{
		{ID: "existing", Type: "page"},
		{ID: "closing-target-123456", Type: "page"},
	})
	spawner.ProcessTargets(context.Background(), []PageTarget{{ID: "existing", Type: "page"}})

	if len(client.cleanupRequests) != 1 {
		t.Fatalf("cleanup requests = %#v, want exactly one pane cleanup", client.cleanupRequests)
	}
	wantPaneID := "chrome-tab-network-closing-targ"
	if !reflect.DeepEqual(client.cleanupRequests[0].PaneIDs, []string{wantPaneID}) {
		t.Fatalf("cleanup PaneIDs = %#v, want %q", client.cleanupRequests[0].PaneIDs, wantPaneID)
	}
	if len(client.createRequests) != 1 || client.createRequests[0].ID != wantPaneID {
		t.Fatalf("create requests = %#v, want child pane %q before cleanup", client.createRequests, wantPaneID)
	}
}

func TestTargetPaneSpawnerForgetsClosedTargetWhenPaneAlreadyDisappeared(t *testing.T) {
	client := &fakePaneClient{cleanupErr: errors.New("transport failed")}
	spawner := newTargetPaneSpawner(trackerConfig{
		Port:    9333,
		RoleBin: "/tmp/bin/zellij-agent",
		Session: "chrome-task",
	}, client, 9, "/repo", io.Discard, io.Discard)

	spawner.ProcessTargets(context.Background(), []PageTarget{{ID: "closing-target-123456", Type: "page"}})
	spawner.ProcessTargets(context.Background(), nil)
	spawner.ProcessTargets(context.Background(), nil)

	if len(client.cleanupRequests) != 1 {
		t.Fatalf("cleanup requests = %#v, want one cleanup attempt for disappeared pane", client.cleanupRequests)
	}
	if len(spawner.active) != 0 {
		t.Fatalf("active = %#v, want disappeared pane forgotten", spawner.active)
	}
}

func TestSelectTargetUsesExplicitTargetID(t *testing.T) {
	targets := []PageTarget{
		{ID: "other", Type: "page", URL: "https://example.com/other"},
		{ID: "wanted", Type: "page", URL: "https://example.com/wanted"},
	}

	got, err := selectTarget(targets, "wanted")
	if err != nil {
		t.Fatalf("selectTarget() error = %v", err)
	}
	if got.ID != "wanted" {
		t.Fatalf("selected target ID = %q, want wanted", got.ID)
	}
}

func intPtr(v int) *int {
	return &v
}

type fakePaneClient struct {
	runtime          transport.InspectRuntimeResponse
	runtimeResponses []transport.InspectRuntimeResponse
	inspectCalls     int
	createRequests   []transport.CreatePaneRequest
	createErr        error
	cleanupRequests  []transport.CleanupRequest
	cleanupErr       error
}

func (f *fakePaneClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	f.inspectCalls++
	if len(f.runtimeResponses) > 0 {
		response := f.runtimeResponses[0]
		f.runtimeResponses = f.runtimeResponses[1:]
		return response, nil
	}
	return f.runtime, nil
}

func (f *fakePaneClient) CreatePane(_ context.Context, req transport.CreatePaneRequest) (transport.CreatePaneResponse, error) {
	f.createRequests = append(f.createRequests, req)
	if f.createErr != nil {
		return transport.CreatePaneResponse{}, f.createErr
	}
	return transport.CreatePaneResponse{Pane: transport.Pane{ID: req.ID}}, nil
}

func (f *fakePaneClient) Cleanup(_ context.Context, req transport.CleanupRequest) (transport.CleanupResponse, error) {
	f.cleanupRequests = append(f.cleanupRequests, req)
	if f.cleanupErr != nil {
		return transport.CleanupResponse{}, f.cleanupErr
	}
	return transport.CleanupResponse{}, nil
}

func TestSelectTargetRejectsMissingExplicitTargetID(t *testing.T) {
	_, err := selectTarget([]PageTarget{{ID: "other", Type: "page"}}, "missing")
	if err == nil {
		t.Fatal("selectTarget() error = nil, want missing target error")
	}
	if !strings.Contains(err.Error(), `Chrome target "missing" not found`) {
		t.Fatalf("selectTarget() error = %v, want missing target message", err)
	}
}

func TestSelectTargetDefaultsToFirstPageTarget(t *testing.T) {
	targets := []PageTarget{
		{ID: "worker", Type: "service_worker", URL: "https://example.com/sw.js"},
		{ID: "first-page", Type: "page", URL: "https://example.com/first"},
		{ID: "second-page", Type: "page", URL: "https://example.com/second"},
	}

	got, err := selectTarget(targets, "")
	if err != nil {
		t.Fatalf("selectTarget() error = %v", err)
	}
	if got.ID != "first-page" {
		t.Fatalf("selected target ID = %q, want first-page", got.ID)
	}
}

func TestSelectTargetRejectsNoPageTargets(t *testing.T) {
	_, err := selectTarget([]PageTarget{{ID: "worker", Type: "service_worker"}}, "")
	if err == nil {
		t.Fatal("selectTarget() error = nil, want no page target error")
	}
	if !strings.Contains(err.Error(), "no attachable Chrome page targets found") {
		t.Fatalf("selectTarget() error = %v, want no page target message", err)
	}
}

func TestSelectOrCreateTargetCreatesAboutBlankWhenNoPageTargets(t *testing.T) {
	var called bool
	got, err := selectOrCreateTarget(context.Background(), []PageTarget{{ID: "worker", Type: "service_worker"}}, "", nil, func(context.Context) (PageTarget, error) {
		called = true
		return PageTarget{ID: "created", Type: "page", URL: "about:blank"}, nil
	})
	if err != nil {
		t.Fatalf("selectOrCreateTarget() error = %v", err)
	}
	if !called {
		t.Fatal("target creator was not called")
	}
	if got.ID != "created" || got.URL != "about:blank" {
		t.Fatalf("created target = %#v, want about:blank page target", got)
	}
}

func TestSelectOrCreateTargetWaitsForLaunchPageBeforeCreating(t *testing.T) {
	var createCalled bool
	got, err := selectOrCreateTarget(
		context.Background(),
		[]PageTarget{{ID: "worker", Type: "service_worker"}},
		"",
		func(context.Context) (PageTarget, bool, error) {
			return PageTarget{ID: "launch-page", Type: "page", URL: "about:blank"}, true, nil
		},
		func(context.Context) (PageTarget, error) {
			createCalled = true
			return PageTarget{ID: "created", Type: "page", URL: "about:blank"}, nil
		},
	)
	if err != nil {
		t.Fatalf("selectOrCreateTarget() error = %v", err)
	}
	if createCalled {
		t.Fatal("target creator was called, want launched page target to be reused")
	}
	if got.ID != "launch-page" {
		t.Fatalf("selected target ID = %q, want launch-page", got.ID)
	}
}

func TestSelectOrCreateTargetDoesNotCreateForExplicitMissingTarget(t *testing.T) {
	_, err := selectOrCreateTarget(context.Background(), []PageTarget{{ID: "worker", Type: "service_worker"}}, "missing", func(context.Context) (PageTarget, bool, error) {
		t.Fatal("target waiter should not be called for explicit target-id")
		return PageTarget{}, false, nil
	}, func(context.Context) (PageTarget, error) {
		t.Fatal("target creator should not be called for explicit target-id")
		return PageTarget{}, nil
	})
	if err == nil {
		t.Fatal("selectOrCreateTarget() error = nil, want missing explicit target error")
	}
	if !strings.Contains(err.Error(), `Chrome target "missing" not found`) {
		t.Fatalf("selectOrCreateTarget() error = %v, want missing target message", err)
	}
}

func TestCreatePageTargetSendsPutAndParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("request method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/json/new" || r.URL.RawQuery != "about:blank" {
			t.Fatalf("request URL = %s?%s, want /json/new?about:blank", r.URL.Path, r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"id":"created","type":"page","title":"New Tab","url":"about:blank"}`)
	}))
	defer server.Close()

	got, err := createPageTargetAt(context.Background(), server.URL, "about:blank", server.Client())
	if err != nil {
		t.Fatalf("createPageTargetAt() error = %v", err)
	}
	if got.ID != "created" || got.Type != "page" || got.URL != "about:blank" {
		t.Fatalf("created target = %#v, want parsed page target", got)
	}
}

func TestPageTargetsFromChromeJSONList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/json/list" {
			t.Fatalf("request path = %s, want /json/list", r.URL.Path)
		}
		fmt.Fprint(w, `[
			{"id":"page-1","type":"page","title":"New Tab","url":"about:blank"},
			{"id":"worker-1","type":"service_worker","title":"Worker","url":"chrome-extension://worker"}
		]`)
	}))
	defer server.Close()

	got, err := pageTargetsFrom(context.Background(), server.URL, server.Client())
	if err != nil {
		t.Fatalf("pageTargetsFrom() error = %v", err)
	}
	want := []PageTarget{
		{ID: "page-1", Type: "page", Title: "New Tab", URL: "about:blank"},
		{ID: "worker-1", Type: "service_worker", Title: "Worker", URL: "chrome-extension://worker"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestTargetStillOpenFindsSelectedPageTarget(t *testing.T) {
	targets := []PageTarget{
		{ID: "wanted", Type: "page", URL: "https://example.com/next"},
	}

	got, ok := targetStillOpen(targets, "wanted")
	if !ok {
		t.Fatal("targetStillOpen() ok = false, want true")
	}
	if got.URL != "https://example.com/next" {
		t.Fatalf("target URL = %q, want updated URL", got.URL)
	}
}

func TestTargetStillOpenRejectsClosedOrNonPageTarget(t *testing.T) {
	if _, ok := targetStillOpen([]PageTarget{{ID: "wanted", Type: "service_worker"}}, "wanted"); ok {
		t.Fatal("targetStillOpen() ok = true for non-page target, want false")
	}
	if _, ok := targetStillOpen([]PageTarget{{ID: "other", Type: "page"}}, "wanted"); ok {
		t.Fatal("targetStillOpen() ok = true for missing target, want false")
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

func TestRequestStoreDedupesByMethodAndFullURL(t *testing.T) {
	store := newRequestStore()
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Second)

	store.Upsert(networkEvent{
		Kind:       eventRequest,
		Method:     "GET",
		URL:        "https://example.com/api/users?id=1",
		ObservedAt: first,
		RequestID:  "request-1",
		TargetID:   "target-1",
	})
	store.Upsert(networkEvent{
		Kind:        eventResponse,
		Method:      "GET",
		URL:         "https://example.com/api/users?id=1",
		Status:      201,
		ContentType: "application/json",
		ObservedAt:  second,
		RequestID:   "request-2",
		TargetID:    "target-2",
	})

	rows := store.Rows()
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Count != 2 || row.Status != 201 || row.ContentType != "application/json" || row.LastRequestID != "request-2" || row.TargetID != "target-2" {
		t.Fatalf("row = %#v, want merged latest response data", row)
	}
	if !row.FirstSeen.Equal(first) || !row.LastSeen.Equal(second) {
		t.Fatalf("row times = %s/%s, want %s/%s", row.FirstSeen, row.LastSeen, first, second)
	}
}

func TestRequestStoreSeparatesDifferentMethods(t *testing.T) {
	store := newRequestStore()
	now := time.Now()

	store.Upsert(networkEvent{Kind: eventRequest, Method: "GET", URL: "https://example.com/api", ObservedAt: now})
	store.Upsert(networkEvent{Kind: eventRequest, Method: "POST", URL: "https://example.com/api", ObservedAt: now})

	if rows := store.Rows(); len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
}

func TestRequestStoreRowsSortOldestFirst(t *testing.T) {
	store := newRequestStore()
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/newer", ObservedAt: first.Add(time.Second)})
	store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/older", ObservedAt: first})

	rows := store.Rows()
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0].URL != "https://example.com/api/older" {
		t.Fatalf("first row URL = %q, want older request first", rows[0].URL)
	}
	if rows[1].URL != "https://example.com/api/newer" {
		t.Fatalf("second row URL = %q, want newer request last", rows[1].URL)
	}
}

func TestRequestStoreCountsRequestAndResponseWithSameRequestIDOnce(t *testing.T) {
	store := newRequestStore()
	now := time.Now()

	store.Upsert(networkEvent{Kind: eventRequest, Method: "GET", URL: "https://example.com/api", RequestID: "same-request", ObservedAt: now})
	store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api", RequestID: "same-request", Status: 200, ObservedAt: now.Add(time.Millisecond)})

	rows := store.Rows()
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Count != 1 {
		t.Fatalf("Count = %d, want 1 for request/response pair", rows[0].Count)
	}
	if rows[0].Status != 200 {
		t.Fatalf("Status = %d, want latest response status 200", rows[0].Status)
	}
}

func TestRequestStorePrunesOldestRowsAtLimit(t *testing.T) {
	store := newRequestStoreWithMaxRows(2)
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	store.Upsert(networkEvent{Kind: eventRequest, Method: "GET", URL: "https://example.com/api/old", RequestID: "old-request", ObservedAt: first})
	store.Upsert(networkEvent{Kind: eventRequest, Method: "GET", URL: "https://example.com/api/mid", RequestID: "mid-request", ObservedAt: first.Add(time.Second)})
	store.Upsert(networkEvent{Kind: eventRequest, Method: "GET", URL: "https://example.com/api/new", RequestID: "new-request", ObservedAt: first.Add(2 * time.Second)})

	rows := store.Rows()
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0].URL != "https://example.com/api/mid" || rows[1].URL != "https://example.com/api/new" {
		t.Fatalf("rows = %#v, want oldest pruned", rows)
	}
	if store.seenRequest["old-request"] {
		t.Fatalf("old request id still tracked after pruning")
	}
}

func TestRequestStoreMergesHeadersAndResponseBody(t *testing.T) {
	store := newRequestStore()
	now := time.Now()

	store.Upsert(networkEvent{
		Kind:           eventRequest,
		Method:         "POST",
		URL:            "https://example.com/api",
		RequestID:      "request-1",
		RequestHeaders: map[string]string{"Authorization": "Bearer test", "Content-Type": "application/json"},
		ObservedAt:     now,
	})
	store.Upsert(networkEvent{
		Kind:            eventResponse,
		Method:          "POST",
		URL:             "https://example.com/api",
		RequestID:       "request-1",
		Status:          201,
		ResponseHeaders: map[string]string{"X-Request-ID": "abc"},
		ResponseBody:    `{"ok":true}`,
		ObservedAt:      now.Add(time.Millisecond),
	})

	rows := store.Rows()
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.RequestHeaders["Authorization"] != "Bearer test" {
		t.Fatalf("RequestHeaders = %#v, want Authorization header", row.RequestHeaders)
	}
	if row.ResponseHeaders["X-Request-ID"] != "abc" {
		t.Fatalf("ResponseHeaders = %#v, want X-Request-ID header", row.ResponseHeaders)
	}
	if row.ResponseBody != `{"ok":true}` {
		t.Fatalf("ResponseBody = %q, want response body", row.ResponseBody)
	}
}

func TestNetworkEventDoesNotFetchResponseBodyBeforeDetail(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})

	updated, _ := model.Update(networkEvent{
		Kind:      eventResponse,
		Method:    "GET",
		URL:       "https://example.com/api",
		RequestID: "request-1",
		TargetID:  "target-1",
		Status:    200,
	})
	next := updated.(trackerModel)

	if len(next.rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(next.rows))
	}
	if next.rows[0].ResponseBody != "" {
		t.Fatalf("ResponseBody = %q, want empty before detail", next.rows[0].ResponseBody)
	}
}

func TestEnteringDetailDoesNotRequestSelectedResponseBody(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{
		Kind:      eventResponse,
		Method:    "GET",
		URL:       "https://example.com/api",
		RequestID: "request-1",
		TargetID:  "target-1",
		Status:    200,
	})
	model.syncRows()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	if !detail.DetailMode {
		t.Fatal("DetailMode = false, want true")
	}
	if cmd != nil {
		t.Fatal("entering detail returned a command, want no lazy body request")
	}
}

func TestModelEnterShowsSelectedDetailAndEscReturnsToList(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api", Status: 200, ObservedAt: time.Now()})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	entered := updated.(trackerModel)
	if entered.DetailMode != true {
		t.Fatal("DetailMode = false, want true after enter")
	}

	updated, _ = entered.Update(tea.KeyMsg{Type: tea.KeyEsc})
	exited := updated.(trackerModel)
	if exited.DetailMode != false {
		t.Fatal("DetailMode = true, want false after esc")
	}
}

func TestModelViewUsesWindowHeight(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	resized := updated.(trackerModel)

	lines := strings.Split(strings.TrimRight(resized.View(), "\n"), "\n")
	if len(lines) != 24 {
		t.Fatalf("View line count = %d, want 24", len(lines))
	}
}

func TestModelViewShowsCurrentURLAtTop(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.applyTabEvent(tabEvent{
		Kind:     tabCreated,
		Target:   PageTarget{ID: "target-1", Type: "page", URL: "https://example.com/current"},
		TargetID: "target-1",
	})

	view := model.View()
	if !strings.Contains(view, "current-url=https://example.com/current") {
		t.Fatalf("View() missing current URL: %q", view)
	}
}

func TestDetailViewUsesTwoPanesForRequestHeadersAndCallResult(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 32
	model.store.Upsert(networkEvent{
		Kind:           eventResponse,
		Method:         "GET",
		URL:            "https://example.com/api",
		Status:         200,
		RequestHeaders: map[string]string{"Accept": "application/json"},
		ResponseHeaders: map[string]string{
			"Content-Type": "application/json",
		},
		ResponseBody: `{"ok":true}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel).View()

	if !strings.Contains(detail, "REQUEST HEADERS") {
		t.Fatalf("detail view missing request pane: %q", detail)
	}
	if !strings.Contains(detail, "CALL RESULT") {
		t.Fatalf("detail view missing result pane: %q", detail)
	}
	if !strings.Contains(detail, "Accept: application/json") {
		t.Fatalf("detail view missing request header: %q", detail)
	}
	if !strings.Contains(detail, `"ok": true`) {
		t.Fatalf("detail view missing response body: %q", detail)
	}
}

func TestFormatResponseBodyPrettyPrintsJSONWithoutTruncation(t *testing.T) {
	body := `{"items":[{"id":1},{"id":2}],"tail":"last-value"}`

	got := formatResponseBody(body)

	if !strings.Contains(got, "{\n") {
		t.Fatalf("formatted body is not pretty JSON: %q", got)
	}
	if !strings.Contains(got, `  "tail": "last-value"`) {
		t.Fatalf("formatted body missing tail value: %q", got)
	}
	if strings.Contains(got, "...") {
		t.Fatalf("formatted body is truncated: %q", got)
	}
}

func TestDetailViewCanScrollToFullResponseBody(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 12
	model.store.Upsert(networkEvent{
		Kind:         eventResponse,
		Method:       "GET",
		URL:          "https://example.com/api",
		Status:       200,
		ResponseBody: `{"lines":["line-00","line-01","line-02","line-03","line-04","line-05","line-06","line-07","line-08","line-09"],"tail":"last-value"}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	firstPage := detail.View()
	if strings.Contains(firstPage, "last-value") {
		t.Fatalf("first detail page unexpectedly contains tail before scrolling: %q", firstPage)
	}

	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	detail = updated.(trackerModel)
	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	bottom := updated.(trackerModel)
	lastPage := bottom.View()
	if !strings.Contains(lastPage, "last-value") {
		t.Fatalf("bottom detail page missing tail value: %q", lastPage)
	}
}

func TestDetailViewScrollsWithJAndK(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 12
	model.store.Upsert(networkEvent{
		Kind:         eventResponse,
		Method:       "GET",
		URL:          "https://example.com/api",
		Status:       200,
		ResponseBody: `{"lines":["line-00","line-01","line-02","line-03","line-04","line-05","line-06","line-07","line-08","line-09"],"tail":"last-value"}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	detail = updated.(trackerModel)
	before := detail.View()

	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	scrolled := updated.(trackerModel)
	afterJ := scrolled.View()
	if scrolled.detailScroll != 1 {
		t.Fatalf("detailScroll after j = %d, want 1", scrolled.detailScroll)
	}
	if afterJ == before {
		t.Fatal("detail view did not change after j")
	}
	if !strings.Contains(afterJ, "detail-scroll 2-") {
		t.Fatalf("detail view missing updated scroll indicator: %q", afterJ)
	}

	updated, _ = scrolled.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	back := updated.(trackerModel)
	if back.detailScroll != 0 {
		t.Fatalf("detailScroll after k = %d, want 0", back.detailScroll)
	}
}

func TestDetailViewScrollsOnlyFocusedPane(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 12
	model.store.Upsert(networkEvent{
		Kind:         eventResponse,
		Method:       "GET",
		URL:          "https://example.com/api",
		Status:       200,
		ResponseBody: `{"lines":["line-00","line-01","line-02","line-03","line-04","line-05","line-06","line-07","line-08","line-09"],"tail":"last-value"}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	requestFocused := updated.(trackerModel)
	updated, _ = requestFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	stillTop := updated.(trackerModel)
	if stillTop.detailRightScroll != 0 {
		t.Fatalf("detailRightScroll after request-pane j = %d, want 0", stillTop.detailRightScroll)
	}

	updated, _ = stillTop.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	resultFocused := updated.(trackerModel)
	updated, _ = resultFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	scrolled := updated.(trackerModel)
	if scrolled.detailRightScroll != 1 {
		t.Fatalf("detailRightScroll after result-pane j = %d, want 1", scrolled.detailRightScroll)
	}
	if scrolled.detailLeftScroll != stillTop.detailLeftScroll {
		t.Fatalf("detailLeftScroll after result-pane j = %d, want preserved %d", scrolled.detailLeftScroll, stillTop.detailLeftScroll)
	}
}

func TestDetailViewFocusesIndependentPanesWithOneAndTwo(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 12
	model.store.Upsert(networkEvent{
		Kind:   eventResponse,
		Method: "POST",
		URL:    "https://example.com/api",
		Status: 500,
		RequestHeaders: map[string]string{
			"X-A": "a",
			"X-B": "b",
			"X-C": "c",
			"X-D": "d",
			"X-E": "e",
		},
		ResponseBody: `{"lines":["line-00","line-01","line-02","line-03","line-04","line-05","line-06","line-07","line-08","line-09"],"tail":"last-value"}`,
		ObservedAt:   time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	if detail.detailPane != detailPaneRequest {
		t.Fatalf("detailPane after enter = %v, want request pane", detail.detailPane)
	}

	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	resultFocused := updated.(trackerModel)
	if resultFocused.detailPane != detailPaneResult {
		t.Fatalf("detailPane after 2 = %v, want result pane", resultFocused.detailPane)
	}

	updated, _ = resultFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	resultScrolled := updated.(trackerModel)
	if resultScrolled.detailRightScroll != 1 {
		t.Fatalf("detailRightScroll after j = %d, want 1", resultScrolled.detailRightScroll)
	}
	if resultScrolled.detailLeftScroll != 0 {
		t.Fatalf("detailLeftScroll after right-pane j = %d, want 0", resultScrolled.detailLeftScroll)
	}

	updated, _ = resultScrolled.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	requestFocused := updated.(trackerModel)
	if requestFocused.detailPane != detailPaneRequest {
		t.Fatalf("detailPane after 1 = %v, want request pane", requestFocused.detailPane)
	}

	updated, _ = requestFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	requestScrolled := updated.(trackerModel)
	if requestScrolled.detailLeftScroll != 1 {
		t.Fatalf("detailLeftScroll after request-pane j = %d, want 1", requestScrolled.detailLeftScroll)
	}
	if requestScrolled.detailRightScroll != 1 {
		t.Fatalf("detailRightScroll after request-pane j = %d, want preserved 1", requestScrolled.detailRightScroll)
	}
}

func TestDetailViewDrawsBorderAroundFocusedPane(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 18
	model.store.Upsert(networkEvent{
		Kind:           eventResponse,
		Method:         "GET",
		URL:            "https://example.com/api",
		Status:         200,
		RequestHeaders: map[string]string{"Accept": "application/json"},
		ResponseBody:   `{"ok":true}`,
		ObservedAt:     time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	requestFocused := updated.(trackerModel)
	requestView := requestFocused.View()
	if !strings.Contains(requestView, "\n╭") {
		t.Fatalf("request-focused detail view missing left pane border: %q", requestView)
	}
	if strings.Contains(requestView, "│ ╭") {
		t.Fatalf("request-focused detail view unexpectedly bordered right pane: %q", requestView)
	}

	updated, _ = requestFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	resultFocused := updated.(trackerModel)
	resultView := resultFocused.View()
	if !strings.Contains(resultView, "│ ╭") {
		t.Fatalf("result-focused detail view missing right pane border: %q", resultView)
	}
}

func TestDetailCopyTextUsesFocusedPane(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{
		Kind:           eventResponse,
		Method:         "POST",
		URL:            "https://example.com/api",
		Status:         201,
		RequestHeaders: map[string]string{"Authorization": "Bearer token"},
		ResponseBody:   `{"ok":true}`,
		ObservedAt:     time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	requestFocused := updated.(trackerModel)
	text, label := requestFocused.focusedDetailCopyText()
	if label != "request" {
		t.Fatalf("copy label = %q, want request", label)
	}
	if !strings.Contains(text, "Authorization: Bearer token") {
		t.Fatalf("request copy text missing request header: %q", text)
	}
	if strings.Contains(text, `"ok": true`) {
		t.Fatalf("request copy text unexpectedly contains response body: %q", text)
	}

	updated, _ = requestFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	resultFocused := updated.(trackerModel)
	text, label = resultFocused.focusedDetailCopyText()
	if label != "result" {
		t.Fatalf("copy label = %q, want result", label)
	}
	if !strings.Contains(text, `"ok": true`) {
		t.Fatalf("result copy text missing response body: %q", text)
	}
	if strings.Contains(text, "Authorization: Bearer token") {
		t.Fatalf("result copy text unexpectedly contains request header: %q", text)
	}
}

func TestCKeyCopiesFocusedDetailPane(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{
		Kind:       eventResponse,
		Method:     "GET",
		URL:        "https://example.com/api",
		Status:     200,
		ObservedAt: time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	updated, cmd := detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	copied := updated.(trackerModel)

	if cmd == nil {
		t.Fatal("c key returned nil command, want clipboard command")
	}
	if copied.copyStatus != "copied request" {
		t.Fatalf("copyStatus = %q, want copied request", copied.copyStatus)
	}
}

func TestLoadingFinishedBuildsResponseBodyEvent(t *testing.T) {
	request := requestSnapshot{Method: "GET", URL: "https://example.com/api"}
	event := responseBodyEventFromFinished(
		context.Background(),
		func(context.Context, network.RequestID) ([]byte, error) {
			return []byte(`{"ok":true}`), nil
		},
		"target-1",
		"request-1",
		request,
		time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC),
	)

	if event.ResponseBody != `{"ok":true}` {
		t.Fatalf("ResponseBody = %q, want fetched body", event.ResponseBody)
	}
	if event.Method != "GET" || event.URL != "https://example.com/api" || event.RequestID != "request-1" || event.TargetID != "target-1" {
		t.Fatalf("event = %#v, want request identity preserved", event)
	}
}

func TestLoadingFinishedFetchesBodyWithTargetContext(t *testing.T) {
	type contextKey string
	targetCtx := context.WithValue(context.Background(), contextKey("kind"), "target")
	request := requestSnapshot{Method: "GET", URL: "https://example.com/api"}
	var gotContext string

	event := responseBodyEventFromLoadingFinished(
		targetCtx,
		func(ctx context.Context, _ network.RequestID) ([]byte, error) {
			value, _ := ctx.Value(contextKey("kind")).(string)
			gotContext = value
			return []byte(`{"ok":true}`), nil
		},
		"target-1",
		"request-1",
		request,
		time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC),
	)

	if gotContext != "target" {
		t.Fatalf("body fetch context = %q, want target context", gotContext)
	}
	if event.ResponseBody != `{"ok":true}` {
		t.Fatalf("ResponseBody = %q, want fetched body", event.ResponseBody)
	}
}

func TestFetchResponseBodyRunsInsideChromedpActionContext(t *testing.T) {
	var runnerCalled bool
	body, err := fetchResponseBodyWithRunner(
		context.Background(),
		"request-1",
		func(ctx context.Context, actions ...chromedp.Action) error {
			runnerCalled = true
			if len(actions) != 1 {
				t.Fatalf("len(actions) = %d, want 1", len(actions))
			}
			return actions[0].Do(cdp.WithExecutor(ctx, fakeResponseBodyExecutor{body: `{"ok":true}`}))
		},
	)
	if err != nil {
		t.Fatalf("fetchResponseBodyWithRunner() error = %v", err)
	}
	if !runnerCalled {
		t.Fatal("runner was not called")
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q, want fetched body", body)
	}
}

type fakeResponseBodyExecutor struct {
	body string
}

func (e fakeResponseBodyExecutor) Execute(_ context.Context, method string, _ any, res any) error {
	if method != network.CommandGetResponseBody {
		return errors.New("unexpected method: " + method)
	}
	out, ok := res.(*network.GetResponseBodyReturns)
	if !ok {
		return errors.New("unexpected result type")
	}
	out.Body = e.body
	return nil
}

func TestLoadingFinishedBodyFetchErrorIsShownAsBodyError(t *testing.T) {
	request := requestSnapshot{Method: "GET", URL: "https://example.com/api"}
	event := responseBodyEventFromFinished(
		context.Background(),
		func(context.Context, network.RequestID) ([]byte, error) {
			return nil, errors.New("body unavailable")
		},
		"target-1",
		"request-1",
		request,
		time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC),
	)

	if event.BodyError != "body unavailable" {
		t.Fatalf("BodyError = %q, want fetch error", event.BodyError)
	}
}

func TestEKeyLoopsThroughErrorAPIs(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/ok", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/bad", Status: 500, ObservedAt: first.Add(time.Second)})
	model.store.Upsert(networkEvent{Kind: eventFailure, Method: "GET", URL: "https://example.com/api/failed", ErrorText: "net::ERR_FAILED", ObservedAt: first.Add(2 * time.Second)})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	firstError := updated.(trackerModel)
	if firstError.rows[firstError.selected].URL != "https://example.com/api/bad" {
		t.Fatalf("selected after first e = %q, want first error API", firstError.rows[firstError.selected].URL)
	}

	updated, _ = firstError.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	secondError := updated.(trackerModel)
	if secondError.rows[secondError.selected].URL != "https://example.com/api/failed" {
		t.Fatalf("selected after second e = %q, want second error API", secondError.rows[secondError.selected].URL)
	}

	updated, _ = secondError.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	wrapped := updated.(trackerModel)
	if wrapped.rows[wrapped.selected].URL != "https://example.com/api/bad" {
		t.Fatalf("selected after wrapped e = %q, want first error API", wrapped.rows[wrapped.selected].URL)
	}
}

func TestFKeyFiltersRowsInRealtime(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/users", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "POST", URL: "https://example.com/api/orders", Status: 201, ObservedAt: first.Add(time.Second)})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	filtering := updated.(trackerModel)
	if !filtering.filterInputActive {
		t.Fatal("filterInputActive = false, want true after f")
	}

	updated, _ = filtering.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("orders")})
	filtered := updated.(trackerModel)
	if filtered.uiFilter != "orders" {
		t.Fatalf("uiFilter = %q, want orders", filtered.uiFilter)
	}
	if len(filtered.rows) != 1 || filtered.rows[0].URL != "https://example.com/api/orders" {
		t.Fatalf("filtered rows = %#v, want only orders API", filtered.rows)
	}

	updated, _ = filtered.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	lessFiltered := updated.(trackerModel)
	if lessFiltered.uiFilter != "order" {
		t.Fatalf("uiFilter after backspace = %q, want order", lessFiltered.uiFilter)
	}

	updated, _ = lessFiltered.Update(tea.KeyMsg{Type: tea.KeyEsc})
	exited := updated.(trackerModel)
	if exited.filterInputActive {
		t.Fatal("filterInputActive = true, want false after esc")
	}
}

func TestDetailFilterDoesNotChangeListFilterOrLeaveDetail(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/users", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{
		Kind:           eventResponse,
		Method:         "POST",
		URL:            "https://example.com/api/orders",
		Status:         201,
		RequestHeaders: map[string]string{"Authorization": "Bearer token"},
		ObservedAt:     first.Add(time.Second),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	filteringList := updated.(trackerModel)
	updated, _ = filteringList.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("orders")})
	listFiltered := updated.(trackerModel)
	if len(listFiltered.rows) != 1 || listFiltered.rows[0].URL != "https://example.com/api/orders" {
		t.Fatalf("list-filtered rows = %#v, want only orders API", listFiltered.rows)
	}

	updated, _ = listFiltered.Update(tea.KeyMsg{Type: tea.KeyEnter})
	listReady := updated.(trackerModel)
	updated, _ = listReady.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	filteringDetail := updated.(trackerModel)
	if !filteringDetail.DetailMode {
		t.Fatal("DetailMode = false after detail f, want to stay in detail")
	}

	updated, _ = filteringDetail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("authorization")})
	filteredDetail := updated.(trackerModel)
	if filteredDetail.uiFilter != "orders" {
		t.Fatalf("list uiFilter = %q, want preserved orders", filteredDetail.uiFilter)
	}
	if len(filteredDetail.rows) != 1 || filteredDetail.rows[0].URL != "https://example.com/api/orders" {
		t.Fatalf("rows after detail filter = %#v, want preserved list filter result", filteredDetail.rows)
	}
	if !filteredDetail.DetailMode {
		t.Fatal("DetailMode = false after detail filter input, want true")
	}
}

func TestDetailFiltersAreScopedPerPane(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 32
	model.store.Upsert(networkEvent{
		Kind:           eventResponse,
		Method:         "POST",
		URL:            "https://example.com/api/orders",
		Status:         201,
		ContentType:    "application/json",
		RequestHeaders: map[string]string{"Authorization": "Bearer token", "X-Trace": "trace-1"},
		ResponseBody:   `{"invoice":"INV-1","status":"created"}`,
		ObservedAt:     time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	updated, _ = detail.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	filteringRequest := updated.(trackerModel)
	updated, _ = filteringRequest.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("authorization")})
	requestFiltered := updated.(trackerModel)
	requestView := requestFiltered.View()
	if !strings.Contains(requestView, "Authorization: Bearer token") {
		t.Fatalf("request-filtered view missing Authorization header: %q", requestView)
	}
	if strings.Contains(requestView, "X-Trace: trace-1") {
		t.Fatalf("request-filtered view still shows non-matching request header: %q", requestView)
	}
	if !strings.Contains(requestView, `"invoice": "INV-1"`) {
		t.Fatalf("request filter should not hide result pane body: %q", requestView)
	}

	updated, _ = requestFiltered.Update(tea.KeyMsg{Type: tea.KeyEnter})
	requestReady := updated.(trackerModel)
	updated, _ = requestReady.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	resultPane := updated.(trackerModel)
	if resultPane.detailFilterForActivePane() != "" {
		t.Fatalf("result detail filter = %q, want empty independent filter", resultPane.detailFilterForActivePane())
	}

	updated, _ = resultPane.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	filteringResult := updated.(trackerModel)
	updated, _ = filteringResult.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("invoice")})
	resultFiltered := updated.(trackerModel)
	resultView := resultFiltered.View()
	if !strings.Contains(resultView, `"invoice": "INV-1"`) {
		t.Fatalf("result-filtered view missing invoice body line: %q", resultView)
	}
	if strings.Contains(resultView, "Content-Type: application/json") {
		t.Fatalf("result-filtered view still shows non-matching result metadata: %q", resultView)
	}

	updated, _ = resultFiltered.Update(tea.KeyMsg{Type: tea.KeyEnter})
	resultReady := updated.(trackerModel)
	updated, _ = resultReady.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	requestPane := updated.(trackerModel)
	if requestPane.detailFilterForActivePane() != "authorization" {
		t.Fatalf("request detail filter = %q, want preserved authorization", requestPane.detailFilterForActivePane())
	}
}

func TestCtrlCClearsAppliedFiltersWithoutQuitting(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/users", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{
		Kind:           eventResponse,
		Method:         "POST",
		URL:            "https://example.com/api/orders",
		Status:         201,
		RequestHeaders: map[string]string{"Authorization": "Bearer token"},
		ResponseBody:   `{"invoice":"INV-1"}`,
		ObservedAt:     first.Add(time.Second),
	})
	model.uiFilter = "orders"
	model.detailFilters[detailPaneRequest] = "authorization"
	model.detailFilters[detailPaneResult] = "invoice"
	model.filterInputActive = true
	model.syncRows()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	cleared := updated.(trackerModel)

	if cmd != nil {
		t.Fatal("ctrl+c returned a command, want no quit command")
	}
	if cleared.uiFilter != "" {
		t.Fatalf("uiFilter = %q, want cleared", cleared.uiFilter)
	}
	if cleared.detailFilterForPane(detailPaneRequest) != "" || cleared.detailFilterForPane(detailPaneResult) != "" {
		t.Fatalf("detail filters = %#v, want cleared", cleared.detailFilters)
	}
	if cleared.filterInputActive {
		t.Fatal("filterInputActive = true, want false")
	}
	if len(cleared.rows) != 2 {
		t.Fatalf("len(rows) = %d, want all rows after clearing filters", len(cleared.rows))
	}
}

func TestCtrlCClearsDetailFiltersAndKeepsDetailMode(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{
		Kind:           eventResponse,
		Method:         "POST",
		URL:            "https://example.com/api/orders",
		Status:         201,
		RequestHeaders: map[string]string{"Authorization": "Bearer token"},
		ResponseBody:   `{"invoice":"INV-1"}`,
		ObservedAt:     time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	detail.detailFilters[detailPaneRequest] = "authorization"
	detail.detailFilters[detailPaneResult] = "invoice"
	detail.filterInputActive = true
	detail.detailLeftScroll = 3
	detail.detailRightScroll = 4

	updated, cmd := detail.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	cleared := updated.(trackerModel)

	if cmd != nil {
		t.Fatal("ctrl+c returned a command, want no quit command")
	}
	if !cleared.DetailMode {
		t.Fatal("DetailMode = false, want to stay in detail")
	}
	if cleared.detailFilterForPane(detailPaneRequest) != "" || cleared.detailFilterForPane(detailPaneResult) != "" {
		t.Fatalf("detail filters = %#v, want cleared", cleared.detailFilters)
	}
	if cleared.detailLeftScroll != 0 || cleared.detailRightScroll != 0 {
		t.Fatalf("detail scrolls = %d/%d, want reset", cleared.detailLeftScroll, cleared.detailRightScroll)
	}
	if cleared.filterInputActive {
		t.Fatal("filterInputActive = true, want false")
	}
}

func TestListViewMarksErrorAPIsForRedRendering(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/bad", Status: 500, ObservedAt: time.Now()})
	model.syncRows()

	view := model.View()

	if !strings.Contains(view, "ERR") {
		t.Fatalf("View() missing error API marker used by red rendering: %q", view)
	}
}

func TestModelLShowsDetailAndHReturnsToList(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api", Status: 200, ObservedAt: time.Now()})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	entered := updated.(trackerModel)
	if entered.DetailMode != true {
		t.Fatal("DetailMode = false, want true after l")
	}

	updated, _ = entered.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	exited := updated.(trackerModel)
	if exited.DetailMode != false {
		t.Fatal("DetailMode = true, want false after h")
	}
}

func TestReturningToListPadsStyledSelectedRowToScreenWidth(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 100
	model.height = 20
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api", Status: 200, ObservedAt: time.Now()})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	entered := updated.(trackerModel)
	updated, _ = entered.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	exited := updated.(trackerModel)

	for _, line := range strings.Split(strings.TrimRight(exited.View(), "\n"), "\n") {
		if !strings.Contains(line, "https://example.com/api") {
			continue
		}
		if got := lipgloss.Width(line); got != model.width {
			t.Fatalf("selected row visible width after returning to list = %d, want %d; line=%q", got, model.width, line)
		}
		return
	}
	t.Fatal("returned list view missing selected API row")
}

func TestListViewUsesAvailableScreenWidthForURL(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 160
	model.height = 20
	longURL := "https://example.com/" + strings.Repeat("a", 90) + "/wide"
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: longURL, Status: 200, ObservedAt: time.Now()})
	model.syncRows()

	view := model.View()

	if !strings.Contains(view, "/wide") {
		t.Fatalf("list view did not use available width for URL suffix: %q", view)
	}
}

func TestDetailViewLinesDoNotExceedScreenWidth(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.width = 60
	model.height = 20
	model.store.Upsert(networkEvent{
		Kind:            eventResponse,
		Method:          "GET",
		URL:             "https://example.com/api",
		Status:          200,
		RequestHeaders:  map[string]string{"Authorization": "Bearer token"},
		ResponseHeaders: map[string]string{"Content-Type": "application/json"},
		ResponseBody:    `{"ok":true}`,
		ObservedAt:      time.Now(),
	})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	detail := updated.(trackerModel)

	for _, line := range detail.visibleDetailLines() {
		if got := lipgloss.Width(line); got > model.width {
			t.Fatalf("detail line visible width = %d, want <= %d; line=%q", got, model.width, line)
		}
	}
}

func TestModelKeepsFocusedAPIWhenEventsReorderRows(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/older", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/newer", Status: 200, ObservedAt: first.Add(time.Second)})
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	focused := updated.(trackerModel)
	if focused.rows[focused.selected].URL != "https://example.com/api/newer" {
		t.Fatalf("selected URL after j = %q, want newer API", focused.rows[focused.selected].URL)
	}

	updated, _ = focused.Update(networkEvent{
		Kind:       eventResponse,
		Method:     "GET",
		URL:        "https://example.com/api/newer",
		Status:     204,
		ObservedAt: first.Add(2 * time.Second),
	})
	reordered := updated.(trackerModel)

	if reordered.rows[reordered.selected].URL != "https://example.com/api/newer" {
		t.Fatalf("selected URL after reorder = %q, want focus to stay on newer API", reordered.rows[reordered.selected].URL)
	}
}

func TestNetworkEventMovesListToLatestFilteredAPI(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.listHeight = 2
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/orders/old", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/users/ignored", Status: 200, ObservedAt: first.Add(time.Second)})
	model.uiFilter = "orders"
	model.syncRows()

	if len(model.rows) != 1 || model.rows[0].URL != "https://example.com/api/orders/old" {
		t.Fatalf("initial filtered rows = %#v, want only old orders API", model.rows)
	}

	updated, _ := model.Update(networkEvent{
		Kind:       eventResponse,
		Method:     "POST",
		URL:        "https://example.com/api/orders/new",
		Status:     201,
		ObservedAt: first.Add(2 * time.Second),
	})
	refreshed := updated.(trackerModel)

	if len(refreshed.rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 filtered orders APIs", len(refreshed.rows))
	}
	if refreshed.selected != len(refreshed.rows)-1 {
		t.Fatalf("selected = %d, want latest index %d", refreshed.selected, len(refreshed.rows)-1)
	}
	if refreshed.rows[refreshed.selected].URL != "https://example.com/api/orders/new" {
		t.Fatalf("selected URL = %q, want latest filtered API", refreshed.rows[refreshed.selected].URL)
	}
}

func TestNetworkEventInDetailDoesNotChangeSelectedAPI(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/selected", Status: 200, ObservedAt: first})
	model.store.Upsert(networkEvent{Kind: eventResponse, Method: "GET", URL: "https://example.com/api/existing", Status: 200, ObservedAt: first.Add(time.Second)})
	model.syncRows()
	model.selected = 0
	model.syncFocus()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	detail := updated.(trackerModel)
	if !detail.DetailMode {
		t.Fatal("DetailMode = false, want true before network event")
	}

	updated, _ = detail.Update(networkEvent{
		Kind:       eventResponse,
		Method:     "GET",
		URL:        "https://example.com/api/latest",
		Status:     200,
		ObservedAt: first.Add(2 * time.Second),
	})
	refreshed := updated.(trackerModel)

	if !refreshed.DetailMode {
		t.Fatal("DetailMode = false after network event, want true")
	}
	if refreshed.rows[refreshed.selected].URL != "https://example.com/api/selected" {
		t.Fatalf("selected URL after detail refresh = %q, want original detail API", refreshed.rows[refreshed.selected].URL)
	}
}

func TestModelGJumpsToBottomAndGGJumpsToTop(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.listHeight = 3
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 6; i++ {
		model.store.Upsert(networkEvent{
			Kind:       eventResponse,
			Method:     "GET",
			URL:        "https://example.com/api/" + string(rune('a'+i)),
			Status:     200,
			ObservedAt: first.Add(time.Duration(6-i) * time.Second),
		})
	}
	model.syncRows()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	bottom := updated.(trackerModel)
	if bottom.selected != len(bottom.rows)-1 {
		t.Fatalf("selected after G = %d, want %d", bottom.selected, len(bottom.rows)-1)
	}
	if bottom.scroll != 3 {
		t.Fatalf("scroll after G = %d, want 3", bottom.scroll)
	}

	updated, _ = bottom.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	pending := updated.(trackerModel)
	if pending.selected != bottom.selected {
		t.Fatalf("selected after first g = %d, want unchanged %d", pending.selected, bottom.selected)
	}

	updated, _ = pending.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	top := updated.(trackerModel)
	if top.selected != 0 {
		t.Fatalf("selected after gg = %d, want 0", top.selected)
	}
	if top.scroll != 0 {
		t.Fatalf("scroll after gg = %d, want 0", top.scroll)
	}
}

func TestModelScrollsToKeepFocusedAPIVisible(t *testing.T) {
	model := newTrackerModel(trackerConfig{Port: 9222})
	model.listHeight = 3
	first := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 6; i++ {
		model.store.Upsert(networkEvent{
			Kind:       eventResponse,
			Method:     "GET",
			URL:        "https://example.com/api/" + string(rune('a'+i)),
			Status:     200,
			ObservedAt: first.Add(time.Duration(6-i) * time.Second),
		})
	}
	model.syncRows()

	var updated tea.Model = model
	for i := 0; i < 5; i++ {
		updated, _ = updated.(trackerModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	scrolled := updated.(trackerModel)

	if scrolled.selected != 5 {
		t.Fatalf("selected = %d, want 5", scrolled.selected)
	}
	if scrolled.scroll != 3 {
		t.Fatalf("scroll = %d, want 3 to keep selected row visible", scrolled.scroll)
	}

	visible := scrolled.visibleRows()
	if len(visible) != 3 {
		t.Fatalf("len(visible) = %d, want 3", len(visible))
	}
	if visible[len(visible)-1].key() != scrolled.rows[scrolled.selected].key() {
		t.Fatalf("focused row is not visible: visible=%#v focused=%#v", visible, scrolled.rows[scrolled.selected])
	}
}
