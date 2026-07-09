package tabnetwork

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdptarget "github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

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

type RequestFilter struct {
	Method      string
	URLContains string
}

type trackerConfig struct {
	Port          int
	ChromePath    string
	UserDataDir   string
	LaunchChrome  bool
	TargetID      string
	Filter        RequestFilter
	ListTargets   bool
	SocketPath    string
	RoleBin       string
	Session       string
	SpawnOnNewTab bool
}

type requestSnapshot struct {
	Method  string
	URL     string
	Headers map[string]string
}

type responseBodyFetcher func(context.Context, network.RequestID) ([]byte, error)

func Run(args []string) int {
	return runWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opts, err := parseOptionsWithOutput(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runTracker(ctx, opts, stdin, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func parseOptions(args []string) (trackerConfig, error) {
	return parseOptionsWithOutput(args, io.Discard)
}

func parseOptionsWithOutput(args []string, output io.Writer) (trackerConfig, error) {
	opts := trackerConfig{
		Port:         9222,
		UserDataDir:  defaultUserDataDir,
		LaunchChrome: true,
		SocketPath:   cli.DefaultSocketPath,
		RoleBin:      "zellij-agent",
		Session:      "chrome",
	}
	fs := flag.NewFlagSet("tab-network", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.IntVar(&opts.Port, "port", opts.Port, "Chrome remote debugging port")
	fs.StringVar(&opts.ChromePath, "chrome-path", "", "Chrome executable path")
	fs.StringVar(&opts.UserDataDir, "user-data-dir", opts.UserDataDir, "Chrome user data directory used when launching Chrome")
	noLaunch := fs.Bool("no-launch", false, "do not launch Chrome; attach to an already running debug port")
	fs.StringVar(&opts.TargetID, "target-id", "", "Chrome page target ID to track")
	fs.StringVar(&opts.Filter.URLContains, "filter-url", "", "show only requests/responses whose URL contains this text")
	fs.StringVar(&opts.Filter.Method, "method", "", "show only requests with this HTTP method")
	fs.BoolVar(&opts.ListTargets, "list", false, "list attachable page targets and exit")
	fs.StringVar(&opts.SocketPath, "socket", opts.SocketPath, "agentd Unix socket path")
	fs.StringVar(&opts.RoleBin, "role-bin", opts.RoleBin, "executable used to run zellij-agent roles")
	fs.StringVar(&opts.Session, "session", opts.Session, "execution session/task id for generated tab panes")
	fs.BoolVar(&opts.SpawnOnNewTab, "spawn-on-new-tab", false, "request a daemon pane in the same Zellij tab when a new Chrome tab opens")
	noSpawn := fs.Bool("no-spawn-on-new-tab", false, "disable daemon pane requests for newly opened Chrome tabs")

	if err := fs.Parse(args); err != nil {
		return trackerConfig{}, err
	}
	if fs.NArg() != 0 {
		return trackerConfig{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return trackerConfig{}, fmt.Errorf("--port must be between 1 and 65535")
	}
	opts.LaunchChrome = !*noLaunch
	if *noSpawn {
		opts.SpawnOnNewTab = false
	}
	opts.TargetID = strings.TrimSpace(opts.TargetID)
	opts.Filter.Method = strings.ToUpper(strings.TrimSpace(opts.Filter.Method))
	return opts, nil
}

func runTracker(ctx context.Context, opts trackerConfig, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var launched *launchedChrome
	if opts.LaunchChrome {
		chrome, err := launchChrome(ctx, opts.ChromePath, opts.Port, opts.UserDataDir)
		if err != nil {
			return err
		}
		launched = chrome
	}
	if err := waitForDebugPort(ctx, opts.Port, 10*time.Second); err != nil {
		return err
	}

	allocatorURL := fmt.Sprintf("http://127.0.0.1:%d", opts.Port)
	allocatorCtx, cancelAllocator := chromedp.NewRemoteAllocator(ctx, allocatorURL)
	defer cancelAllocator()

	targets, err := pageTargets(ctx, opts.Port)
	if err != nil {
		return err
	}
	if opts.ListTargets {
		for _, target := range targets {
			if target.Type == "page" {
				fmt.Fprintf(stdout, "%s\t%s\t%s\n", target.ID, target.Title, target.URL)
			}
		}
		return nil
	}

	target, err := selectOrCreateTarget(
		ctx,
		targets,
		opts.TargetID,
		func(ctx context.Context) (PageTarget, bool, error) {
			return waitForFirstPageTarget(ctx, func(context.Context) ([]PageTarget, error) {
				return pageTargets(ctx, opts.Port)
			}, 2*time.Second, 100*time.Millisecond)
		},
		func(ctx context.Context) (PageTarget, error) {
			return createPageTarget(ctx, opts.Port)
		},
	)
	if err != nil {
		return err
	}
	opts.TargetID = target.ID

	events := make(chan any, 128)
	go collectTarget(ctx, opts.Port, allocatorCtx, target, opts.Filter, events, stderr)
	startBrowserShutdownWatcher(ctx, opts, launched, events, stderr)
	if opts.SpawnOnNewTab {
		startTargetPaneSpawner(ctx, opts, stdout, stderr)
	}

	model := newTrackerModel(opts)
	model.events = events
	if launched != nil && launched.cmd != nil && launched.cmd.Process != nil {
		model.chromePID = launched.cmd.Process.Pid
	}

	program := tea.NewProgram(model, tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}

type pageTargetCreator func(context.Context) (PageTarget, error)
type pageTargetWaiter func(context.Context) (PageTarget, bool, error)

func selectTarget(targets []PageTarget, targetID string) (PageTarget, error) {
	if targetID != "" {
		for _, target := range targets {
			if target.ID == targetID && target.Type == "page" {
				return target, nil
			}
		}
		return PageTarget{}, fmt.Errorf("Chrome target %q not found", targetID)
	}

	for _, target := range targets {
		if target.Type == "page" {
			return target, nil
		}
	}
	return PageTarget{}, errors.New("no attachable Chrome page targets found")
}

func selectOrCreateTarget(ctx context.Context, targets []PageTarget, targetID string, wait pageTargetWaiter, create pageTargetCreator) (PageTarget, error) {
	if targetID != "" {
		return selectTarget(targets, targetID)
	}
	for _, target := range targets {
		if target.Type == "page" {
			return target, nil
		}
	}
	if wait != nil {
		target, ok, err := wait(ctx)
		if err != nil {
			return PageTarget{}, err
		}
		if ok {
			return target, nil
		}
	}
	if create == nil {
		return PageTarget{}, errors.New("no attachable Chrome page targets found")
	}
	target, err := create(ctx)
	if err != nil {
		return PageTarget{}, fmt.Errorf("create about:blank Chrome target: %w", err)
	}
	if target.ID == "" {
		return PageTarget{}, errors.New("create about:blank Chrome target: response missing target id")
	}
	if target.Type == "" {
		target.Type = "page"
	}
	if target.URL == "" {
		target.URL = "about:blank"
	}
	return target, nil
}

func waitForFirstPageTarget(ctx context.Context, list func(context.Context) ([]PageTarget, error), timeout time.Duration, interval time.Duration) (PageTarget, bool, error) {
	if list == nil {
		return PageTarget{}, false, nil
	}
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		targets, err := list(ctx)
		if err != nil {
			return PageTarget{}, false, err
		}
		for _, target := range targets {
			if target.Type == "page" {
				return target, true, nil
			}
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return PageTarget{}, false, nil
			}
			return PageTarget{}, false, ctx.Err()
		case <-timer.C:
		}
	}
}

func targetStillOpen(targets []PageTarget, targetID string) (PageTarget, bool) {
	for _, target := range targets {
		if target.ID == targetID && target.Type == "page" {
			return target, true
		}
	}
	return PageTarget{}, false
}

func createPageTarget(ctx context.Context, port int) (PageTarget, error) {
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 2 * time.Second}
	return createPageTargetAt(ctx, baseURL, "about:blank", client)
}

func createPageTargetAt(ctx context.Context, baseURL string, targetURL string, client *http.Client) (PageTarget, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/json/new?" + targetURL
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, nil)
	if err != nil {
		return PageTarget{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return PageTarget{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return PageTarget{}, fmt.Errorf("Chrome target creation returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var target PageTarget
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		return PageTarget{}, err
	}
	return target, nil
}

func collectTarget(ctx context.Context, port int, allocatorCtx context.Context, target PageTarget, filter RequestFilter, out chan<- any, stderr io.Writer) {
	trackCtx, cancelTrack := context.WithCancel(ctx)
	defer cancelTrack()

	sendTrackerMsg(ctx, out, tabEvent{Kind: tabCreated, Target: target, ObservedAt: time.Now()})
	go attachAndTrackTarget(trackCtx, allocatorCtx, target, filter, out)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			targets, err := pageTargets(ctx, port)
			if err != nil {
				if ctx.Err() == nil {
					fmt.Fprintf(stderr, "target scan failed: %v\n", err)
				}
				continue
			}
			latest, ok := targetStillOpen(targets, target.ID)
			if !ok {
				cancelTrack()
				sendTrackerMsg(ctx, out, tabEvent{Kind: tabClosed, TargetID: target.ID, ObservedAt: time.Now()})
				sendTrackerMsg(ctx, out, collectorDone{})
				return
			}
			if latest.URL != "" && latest.URL != target.URL {
				target = latest
				sendTrackerMsg(ctx, out, tabEvent{Kind: tabNavigated, Target: target, TargetID: target.ID, URL: target.URL, ObservedAt: time.Now()})
			}
		}
	}
}

type paneClient interface {
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
	CreatePane(context.Context, transport.CreatePaneRequest) (transport.CreatePaneResponse, error)
	Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error)
}

type workflowCleanupClient interface {
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
	Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error)
}

type browserShutdownSupervisor struct {
	config trackerConfig
	client workflowCleanupClient
	events chan<- any
	stderr io.Writer
	once   sync.Once
}

func newBrowserShutdownSupervisor(config trackerConfig, client workflowCleanupClient, events chan<- any, stderr io.Writer) *browserShutdownSupervisor {
	return &browserShutdownSupervisor{
		config: config,
		client: client,
		events: events,
		stderr: stderr,
	}
}

func startBrowserShutdownWatcher(ctx context.Context, opts trackerConfig, launched *launchedChrome, events chan<- any, stderr io.Writer) {
	client := transport.NewClient(transport.ClientOptions{SocketPath: opts.SocketPath, Timeout: 10 * time.Second})
	supervisor := newBrowserShutdownSupervisor(opts, client, events, stderr)
	if launched != nil {
		supervisor.watchLaunchedChrome(ctx, launched.done)
		return
	}
	supervisor.watchDebugEndpoint(ctx, func(ctx context.Context) error {
		return checkDebugEndpoint(ctx, opts.Port)
	}, time.Second, 3)
}

func (s *browserShutdownSupervisor) watchLaunchedChrome(ctx context.Context, done <-chan error) {
	if done == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case err := <-done:
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fmt.Fprintf(s.stderr, "Chrome process exited: %v\n", err)
			}
			s.handleBrowserClosed(ctx)
		}
	}()
}

func (s *browserShutdownSupervisor) watchDebugEndpoint(ctx context.Context, check func(context.Context) error, interval time.Duration, failureThreshold int) {
	if check == nil {
		return
	}
	if interval <= 0 {
		interval = time.Second
	}
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := check(ctx); err != nil {
					failures++
					if failures >= failureThreshold {
						s.handleBrowserClosed(ctx)
						return
					}
					continue
				}
				failures = 0
			}
		}
	}()
}

func (s *browserShutdownSupervisor) handleBrowserClosed(ctx context.Context) {
	s.once.Do(func() {
		if s.config.SpawnOnNewTab && s.client != nil {
			if err := s.cleanupWorkflowChildren(ctx); err != nil {
				fmt.Fprintf(s.stderr, "browser shutdown cleanup failed: %v\n", err)
			}
		}
		sendTrackerMsg(ctx, s.events, collectorDone{})
	})
}

func (s *browserShutdownSupervisor) cleanupWorkflowChildren(ctx context.Context) error {
	childPaneIDs, ownPaneID, ok, err := s.workflowPaneIDs(ctx)
	if err != nil {
		return err
	}
	if !ok {
		_, err := s.client.Cleanup(ctx, transport.CleanupRequest{TaskID: s.config.Session, Role: "tab-network"})
		return err
	}
	var firstErr error
	if len(childPaneIDs) > 0 {
		if _, err := s.client.Cleanup(ctx, transport.CleanupRequest{PaneIDs: childPaneIDs}); err != nil {
			firstErr = err
		}
	}
	if ownPaneID != "" {
		selfCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = s.client.Cleanup(selfCtx, transport.CleanupRequest{PaneIDs: []string{ownPaneID}})
	}
	return firstErr
}

func (s *browserShutdownSupervisor) workflowPaneIDs(ctx context.Context) ([]string, string, bool, error) {
	ownZellijPaneID := normalizeZellijPaneID(os.Getenv("ZELLIJ_PANE_ID"))
	if ownZellijPaneID == "" {
		return nil, "", false, nil
	}
	response, err := s.client.InspectRuntime(ctx)
	if err != nil {
		return nil, "", false, err
	}
	paneIDs := make([]string, 0)
	ownPaneID := ""
	for _, pane := range response.Panes {
		if pane.TaskID != s.config.Session || pane.Role != "tab-network" {
			continue
		}
		if pane.ZellijPaneID == ownZellijPaneID {
			ownPaneID = pane.ID
			continue
		}
		switch pane.Status {
		case "closed", "exited", "lost":
			continue
		}
		paneIDs = append(paneIDs, pane.ID)
	}
	return paneIDs, ownPaneID, true, nil
}

type targetPaneSpawner struct {
	config trackerConfig
	client paneClient
	tabID  int
	cwd    string
	stdout io.Writer
	stderr io.Writer
	seen   map[string]struct{}
	active map[string]string
}

func newTargetPaneSpawner(config trackerConfig, client paneClient, tabID int, cwd string, stdout, stderr io.Writer) *targetPaneSpawner {
	return &targetPaneSpawner{
		config: config,
		client: client,
		tabID:  tabID,
		cwd:    cwd,
		stdout: stdout,
		stderr: stderr,
		seen:   map[string]struct{}{},
		active: map[string]string{},
	}
}

func (s *targetPaneSpawner) MarkBaseline(targets []PageTarget) {
	count := 0
	for _, target := range targets {
		if target.Type != "page" || strings.TrimSpace(target.ID) == "" {
			continue
		}
		s.seen[target.ID] = struct{}{}
		count++
	}
	fmt.Fprintf(s.stdout, "spawn baseline page-targets=%d\n", count)
}

func (s *targetPaneSpawner) ProcessTargets(ctx context.Context, targets []PageTarget) {
	current := map[string]struct{}{}
	for _, target := range targets {
		if target.Type != "page" || strings.TrimSpace(target.ID) == "" {
			continue
		}
		current[target.ID] = struct{}{}
		if _, ok := s.seen[target.ID]; ok {
			continue
		}
		s.seen[target.ID] = struct{}{}
		req := buildChildPaneRequest(s.config, target, s.tabID, s.cwd)
		if _, err := s.client.CreatePane(ctx, req); err != nil {
			fmt.Fprintf(s.stderr, "spawn target=%s failed: %v\n", target.ID, err)
			continue
		}
		s.active[target.ID] = req.ID
		fmt.Fprintf(s.stdout, "spawned target=%s pane=%s\n", target.ID, req.ID)
	}
	s.cleanupClosedTargets(ctx, current)
}

func (s *targetPaneSpawner) cleanupClosedTargets(ctx context.Context, current map[string]struct{}) {
	for targetID, paneID := range s.active {
		if _, ok := current[targetID]; ok {
			continue
		}
		if _, err := s.client.Cleanup(ctx, transport.CleanupRequest{PaneIDs: []string{paneID}}); err != nil {
			if s.paneGone(ctx, paneID) {
				delete(s.active, targetID)
				fmt.Fprintf(s.stdout, "forgot target=%s pane=%s\n", targetID, paneID)
				continue
			}
			fmt.Fprintf(s.stderr, "cleanup target=%s pane=%s failed: %v\n", targetID, paneID, err)
			continue
		}
		delete(s.active, targetID)
		fmt.Fprintf(s.stdout, "cleaned target=%s pane=%s\n", targetID, paneID)
	}
}

func (s *targetPaneSpawner) paneGone(ctx context.Context, paneID string) bool {
	response, err := s.client.InspectRuntime(ctx)
	if err != nil {
		return false
	}
	for _, pane := range response.Panes {
		if pane.ID != paneID {
			continue
		}
		switch pane.Status {
		case "closed", "exited", "lost":
			return true
		default:
			return false
		}
	}
	return true
}

func startTargetPaneSpawner(ctx context.Context, opts trackerConfig, stdout, stderr io.Writer) {
	zellijPaneID := os.Getenv("ZELLIJ_PANE_ID")
	if strings.TrimSpace(zellijPaneID) == "" {
		fmt.Fprintln(stderr, "spawn-on-new-tab disabled: ZELLIJ_PANE_ID is not set")
		return
	}

	client := transport.NewClient(transport.ClientOptions{SocketPath: opts.SocketPath, Timeout: 10 * time.Second})
	parent, err := waitForOwnManagedPane(ctx, client, zellijPaneID, 5*time.Second, 100*time.Millisecond)
	if err != nil {
		fmt.Fprintf(stderr, "spawn-on-new-tab disabled: %v\n", err)
		return
	}
	if parent.ZellijTabID == nil {
		fmt.Fprintln(stderr, "spawn-on-new-tab disabled: parent pane missing zellij tab id")
		return
	}
	cwd := parent.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	spawner := newTargetPaneSpawner(opts, client, *parent.ZellijTabID, cwd, stdout, stderr)
	targets, err := pageTargets(ctx, opts.Port)
	if err != nil {
		fmt.Fprintf(stderr, "spawn-on-new-tab disabled: initial target scan failed: %v\n", err)
		return
	}
	spawner.MarkBaseline(targets)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				targets, err := pageTargets(ctx, opts.Port)
				if err != nil {
					if ctx.Err() == nil {
						fmt.Fprintf(stderr, "spawn target scan failed: %v\n", err)
					}
					continue
				}
				spawner.ProcessTargets(ctx, targets)
			}
		}
	}()
}

func resolveOwnManagedPane(ctx context.Context, client paneClient, zellijPaneID string) (transport.Pane, error) {
	response, err := client.InspectRuntime(ctx)
	if err != nil {
		return transport.Pane{}, err
	}
	zellijPaneID = normalizeZellijPaneID(zellijPaneID)
	for _, pane := range response.Panes {
		if pane.ZellijPaneID != zellijPaneID {
			continue
		}
		if pane.ZellijTabID == nil {
			return transport.Pane{}, fmt.Errorf("managed pane %s missing zellij tab id", pane.ID)
		}
		return pane, nil
	}
	return transport.Pane{}, fmt.Errorf("managed pane with zellij pane id %s not found", zellijPaneID)
}

func waitForOwnManagedPane(ctx context.Context, client paneClient, zellijPaneID string, timeout, interval time.Duration) (transport.Pane, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		pane, err := resolveOwnManagedPane(ctx, client, zellijPaneID)
		if err == nil {
			return pane, nil
		}
		lastErr = err

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if lastErr != nil {
				return transport.Pane{}, lastErr
			}
			return transport.Pane{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func normalizeZellijPaneID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, "terminal_") || strings.HasPrefix(id, "plugin_") {
		return id
	}
	if _, err := strconv.Atoi(id); err == nil {
		return "terminal_" + id
	}
	return id
}

func buildChildPaneRequest(config trackerConfig, target PageTarget, tabID int, cwd string) transport.CreatePaneRequest {
	shortID := shortTargetID(target.ID)
	paneID := "chrome-tab-network-" + shortID
	return transport.CreatePaneRequest{
		ID:          paneID,
		TaskID:      config.Session,
		Role:        "tab-network",
		Name:        paneID,
		ZellijTabID: &tabID,
		CWD:         cwd,
		Command: []string{
			config.RoleBin,
			"role",
			"tab-network",
			"--port",
			strconv.Itoa(config.Port),
			"--no-launch",
			"--target-id",
			target.ID,
			"--no-spawn-on-new-tab",
		},
	}
}

func shortTargetID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func (f RequestFilter) Include(method, url string) bool {
	if f.Method != "" && !strings.EqualFold(f.Method, method) {
		return false
	}
	if f.URLContains != "" && !strings.Contains(url, f.URLContains) {
		return false
	}
	return true
}

func sendTrackerMsg(ctx context.Context, out chan<- any, msg any) {
	select {
	case <-ctx.Done():
	case out <- msg:
	}
}

type launchedChrome struct {
	cmd  *exec.Cmd
	done <-chan error
}

func launchChrome(ctx context.Context, chromePath string, port int, userDataDir string) (*launchedChrome, error) {
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
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	return &launchedChrome{cmd: cmd, done: done}, nil
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

func chromeArgs(port int, userDataDir string) []string {
	return []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	}
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

func checkDebugEndpoint(ctx context.Context, port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	client := http.Client{Timeout: 500 * time.Millisecond}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Chrome debug endpoint returned %s", resp.Status)
	}
	return nil
}

func attachAndTrackTarget(ctx context.Context, allocatorCtx context.Context, target PageTarget, filter RequestFilter, out chan<- any) {
	targetCtx, cancel := chromedp.NewContext(allocatorCtx, chromedp.WithTargetID(cdptarget.ID(target.ID)))
	defer cancel()

	fetchBody := fetchResponseBody

	var mu sync.Mutex
	requests := map[network.RequestID]requestSnapshot{}
	chromedp.ListenTarget(targetCtx, func(event any) {
		now := time.Now()
		switch e := event.(type) {
		case *network.EventRequestWillBeSent:
			mu.Lock()
			requests[e.RequestID] = requestSnapshot{
				Method:  e.Request.Method,
				URL:     e.Request.URL,
				Headers: headersToStrings(e.Request.Headers),
			}
			mu.Unlock()
			if filter.Include(e.Request.Method, e.Request.URL) {
				sendTrackerMsg(ctx, out, networkEvent{
					Kind:           eventRequest,
					Method:         e.Request.Method,
					URL:            e.Request.URL,
					RequestHeaders: headersToStrings(e.Request.Headers),
					RequestID:      string(e.RequestID),
					TargetID:       target.ID,
					ObservedAt:     now,
				})
			}
		case *network.EventResponseReceived:
			mu.Lock()
			request := requests[e.RequestID]
			if request.URL == "" {
				request.URL = e.Response.URL
			}
			requests[e.RequestID] = request
			mu.Unlock()
			if filter.Include(request.Method, request.URL) {
				sendTrackerMsg(ctx, out, networkEvent{
					Kind:            eventResponse,
					Method:          request.Method,
					URL:             request.URL,
					Status:          int64(e.Response.Status),
					ContentType:     e.Response.MimeType,
					RequestHeaders:  request.Headers,
					ResponseHeaders: headersToStrings(e.Response.Headers),
					RequestID:       string(e.RequestID),
					TargetID:        target.ID,
					ObservedAt:      now,
				})
			}
		case *network.EventLoadingFailed:
			mu.Lock()
			request := requests[e.RequestID]
			delete(requests, e.RequestID)
			mu.Unlock()
			if filter.Include(request.Method, request.URL) {
				sendTrackerMsg(ctx, out, networkEvent{
					Kind:       eventFailure,
					Method:     request.Method,
					URL:        request.URL,
					BodyError:  e.ErrorText,
					ErrorText:  e.ErrorText,
					RequestID:  string(e.RequestID),
					TargetID:   target.ID,
					ObservedAt: now,
				})
			}
		case *network.EventLoadingFinished:
			mu.Lock()
			request := requests[e.RequestID]
			delete(requests, e.RequestID)
			mu.Unlock()
			if filter.Include(request.Method, request.URL) {
				go func(requestID network.RequestID, request requestSnapshot) {
					sendTrackerMsg(ctx, out, responseBodyEventFromLoadingFinished(targetCtx, fetchBody, target.ID, requestID, request, time.Now()))
				}(e.RequestID, request)
			}
		case *page.EventFrameNavigated:
			if e.Frame.ParentID == "" {
				sendTrackerMsg(ctx, out, tabEvent{Kind: tabNavigated, TargetID: target.ID, URL: e.Frame.URL, ObservedAt: now})
			}
		case *page.EventNavigatedWithinDocument:
			sendTrackerMsg(ctx, out, tabEvent{Kind: tabNavigated, TargetID: target.ID, URL: e.URL, ObservedAt: now})
		}
	})

	if err := chromedp.Run(targetCtx, network.Enable(), page.Enable()); err != nil && ctx.Err() == nil {
		sendTrackerMsg(ctx, out, errorEvent{Message: fmt.Sprintf("target attach failed %s: %v", target.ID, err)})
		return
	}
	<-ctx.Done()
}

func responseBodyEventFromLoadingFinished(targetCtx context.Context, fetchBody responseBodyFetcher, targetID string, requestID network.RequestID, request requestSnapshot, observedAt time.Time) networkEvent {
	return responseBodyEventFromFinished(targetCtx, fetchBody, targetID, requestID, request, observedAt)
}

func fetchResponseBody(ctx context.Context, requestID network.RequestID) ([]byte, error) {
	return fetchResponseBodyWithRunner(ctx, requestID, chromedp.Run)
}

func fetchResponseBodyWithRunner(ctx context.Context, requestID network.RequestID, run func(context.Context, ...chromedp.Action) error) ([]byte, error) {
	var body []byte
	err := run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		body, err = network.GetResponseBody(requestID).Do(ctx)
		return err
	}))
	return body, err
}

func responseBodyEventFromFinished(ctx context.Context, fetchBody responseBodyFetcher, targetID string, requestID network.RequestID, request requestSnapshot, observedAt time.Time) networkEvent {
	event := networkEvent{
		Kind:       eventResponse,
		Method:     request.Method,
		URL:        request.URL,
		RequestID:  string(requestID),
		TargetID:   targetID,
		ObservedAt: observedAt,
	}
	body, err := fetchBody(ctx, requestID)
	if err != nil {
		event.BodyError = err.Error()
		return event
	}
	if len(body) > 0 {
		event.ResponseBody = string(body)
	}
	return event
}

func headersToStrings(headers network.Headers) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		out[key] = fmt.Sprint(value)
	}
	return out
}

func pageTargets(ctx context.Context, port int) ([]PageTarget, error) {
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 2 * time.Second}
	return pageTargetsFrom(ctx, baseURL, client)
}

func pageTargetsFrom(ctx context.Context, baseURL string, client *http.Client) ([]PageTarget, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/json/list"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Chrome target list returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var targets []PageTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

type networkEventKind int

const (
	eventRequest networkEventKind = iota
	eventResponse
	eventFailure
)

type networkEvent struct {
	Kind            networkEventKind
	Method          string
	URL             string
	Status          int64
	ContentType     string
	ErrorText       string
	BodyError       string
	RequestHeaders  map[string]string
	ResponseHeaders map[string]string
	ResponseBody    string
	RequestID       string
	TargetID        string
	ObservedAt      time.Time
}

type tabEventKind int

const (
	tabCreated tabEventKind = iota
	tabClosed
	tabNavigated
)

type tabEvent struct {
	Kind       tabEventKind
	Target     PageTarget
	TargetID   string
	URL        string
	ObservedAt time.Time
}

type errorEvent struct {
	Message string
}

type requestRow struct {
	Method          string
	URL             string
	Status          int64
	ContentType     string
	Count           int
	FirstSeen       time.Time
	LastSeen        time.Time
	ErrorText       string
	BodyError       string
	RequestHeaders  map[string]string
	ResponseHeaders map[string]string
	ResponseBody    string
	LastRequestID   string
	TargetID        string
}

func (r requestRow) isError() bool {
	return r.Status >= 400 || r.ErrorText != ""
}

func (r requestRow) key() string {
	return requestKey(r.Method, r.URL)
}

type requestStore struct {
	rows        []requestRow
	index       map[string]int
	seenRequest map[string]bool
}

func newRequestStore() *requestStore {
	return &requestStore{
		index:       map[string]int{},
		seenRequest: map[string]bool{},
	}
}

func (s *requestStore) Upsert(event networkEvent) {
	method := strings.ToUpper(strings.TrimSpace(event.Method))
	if method == "" {
		method = "-"
	}
	if event.URL == "" {
		return
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now()
	}

	key := requestKey(method, event.URL)
	idx, ok := s.index[key]
	if !ok {
		idx = len(s.rows)
		s.index[key] = idx
		s.rows = append(s.rows, requestRow{
			Method:    method,
			URL:       event.URL,
			FirstSeen: event.ObservedAt,
		})
	}

	row := s.rows[idx]
	if shouldIncrementCount(event, s.seenRequest, row.Count) {
		row.Count++
	}
	if event.RequestID != "" {
		s.seenRequest[event.RequestID] = true
		row.LastRequestID = event.RequestID
	}
	row.LastSeen = event.ObservedAt
	row.TargetID = event.TargetID
	if event.Status != 0 {
		row.Status = event.Status
	}
	if event.ContentType != "" {
		row.ContentType = event.ContentType
	}
	if len(event.RequestHeaders) > 0 {
		row.RequestHeaders = cloneStringMap(event.RequestHeaders)
	}
	if len(event.ResponseHeaders) > 0 {
		row.ResponseHeaders = cloneStringMap(event.ResponseHeaders)
	}
	if event.ResponseBody != "" {
		row.ResponseBody = event.ResponseBody
		row.BodyError = ""
	}
	if event.BodyError != "" {
		row.BodyError = event.BodyError
	}
	if event.Kind == eventFailure {
		row.ErrorText = event.ErrorText
	} else if event.ErrorText == "" {
		row.ErrorText = ""
	}
	s.rows[idx] = row
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func requestKey(method, url string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + "\x00" + url
}

func shouldIncrementCount(event networkEvent, seen map[string]bool, currentCount int) bool {
	if event.Kind == eventRequest {
		return event.RequestID == "" || !seen[event.RequestID]
	}
	if event.RequestID != "" {
		return !seen[event.RequestID]
	}
	return currentCount == 0
}

func (s *requestStore) Rows() []requestRow {
	out := make([]requestRow, len(s.rows))
	copy(out, s.rows)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastSeen.Before(out[j].LastSeen)
	})
	return out
}

type trackerModel struct {
	config            trackerConfig
	events            <-chan any
	store             *requestStore
	rows              []requestRow
	selected          int
	focusedKey        string
	scroll            int
	listHeight        int
	width             int
	height            int
	detailScroll      int
	detailLeftScroll  int
	detailRightScroll int
	detailPane        detailPane
	uiFilter          string
	detailFilters     map[detailPane]string
	filterInputActive bool
	copyStatus        string
	pendingG          bool
	DetailMode        bool
	tabs              map[string]PageTarget
	lastError         string
	chromePID         int
}

type detailPane int

const (
	detailPaneRequest detailPane = iota + 1
	detailPaneResult
)

func newTrackerModel(config trackerConfig) trackerModel {
	return trackerModel{
		config: config,
		store:  newRequestStore(),
		tabs:   map[string]PageTarget{},
		detailFilters: map[detailPane]string{
			detailPaneRequest: "",
			detailPaneResult:  "",
		},
		detailPane: detailPaneRequest,
	}
}

func (m trackerModel) Init() tea.Cmd {
	if m.events == nil {
		return nil
	}
	return waitForEvent(m.events)
}

func waitForEvent(events <-chan any) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return collectorDone{}
		}
		return msg
	}
}

type collectorDone struct{}

func (m trackerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.filterInputActive {
			switch msg.String() {
			case "ctrl+c":
				m.resetConfiguredState()
			case "esc", "enter":
				m.filterInputActive = false
			case "backspace":
				m.backspaceActiveFilter()
			default:
				if len(msg.Runes) > 0 {
					m.appendActiveFilter(string(msg.Runes))
				}
			}
			m.pendingG = false
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			m.resetConfiguredState()
		case "q":
			return m, tea.Quit
		case "up", "k":
			m.pendingG = false
			if m.DetailMode {
				m.scrollDetail(-1)
			} else if m.selected > 0 {
				m.selected--
				m.syncFocus()
			}
		case "down", "j":
			m.pendingG = false
			if m.DetailMode {
				m.scrollDetail(1)
			} else if m.selected < len(m.rows)-1 {
				m.selected++
				m.syncFocus()
			}
		case "enter", "l":
			m.pendingG = false
			if len(m.rows) > 0 {
				m.DetailMode = true
				m.detailPane = detailPaneRequest
				m.resetDetailScrolls()
			}
		case "esc", "h":
			m.pendingG = false
			m.DetailMode = false
		case "1":
			m.pendingG = false
			if m.DetailMode {
				m.detailPane = detailPaneRequest
				m.syncDetailScrollMirror()
			}
		case "2":
			m.pendingG = false
			if m.DetailMode {
				m.detailPane = detailPaneResult
				m.syncDetailScrollMirror()
			}
		case "c":
			m.pendingG = false
			if m.DetailMode {
				text, label := m.focusedDetailCopyText()
				if text != "" {
					m.copyStatus = "copied " + label
					return m, copyToClipboard(text)
				}
			}
		case "e":
			m.pendingG = false
			m.moveToNextError()
			if m.DetailMode {
				m.resetDetailScrolls()
			}
		case "f":
			m.pendingG = false
			m.filterInputActive = true
		case "G":
			m.pendingG = false
			if m.DetailMode {
				m.scrollDetailToBottom()
			} else {
				m.moveToBottom()
			}
		case "g":
			if m.pendingG {
				if m.DetailMode {
					m.scrollDetailToTop()
				} else {
					m.moveToTop()
				}
				m.pendingG = false
			} else {
				m.pendingG = true
			}
		default:
			m.pendingG = false
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.listHeight = listHeightForWindow(msg.Height)
		m.syncScroll()
		return m, nil
	case networkEvent:
		m.store.Upsert(msg)
		if m.DetailMode {
			m.syncRows()
		} else {
			m.syncRowsToBottom()
		}
		return m, waitForEvent(m.events)
	case tabEvent:
		m.applyTabEvent(msg)
		return m, waitForEvent(m.events)
	case errorEvent:
		m.lastError = msg.Message
		return m, waitForEvent(m.events)
	case clipboardEvent:
		if msg.Err != nil {
			m.lastError = "clipboard copy failed: " + msg.Err.Error()
		}
		return m, nil
	case collectorDone:
		m.lastError = "collector stopped"
		return m, tea.Quit
	}

	return m, nil
}

func (m *trackerModel) syncRows() {
	if m.focusedKey == "" && m.selected >= 0 && m.selected < len(m.rows) {
		m.focusedKey = m.rows[m.selected].key()
	}

	m.rows = m.filteredRows()
	if m.focusedKey != "" {
		for i, row := range m.rows {
			if row.key() == m.focusedKey {
				m.selected = i
				m.syncScroll()
				return
			}
		}
	}

	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	m.syncFocus()
	m.syncScroll()
}

func (m *trackerModel) syncRowsToBottom() {
	m.rows = m.filteredRows()
	if len(m.rows) == 0 {
		m.selected = 0
		m.focusedKey = ""
		m.scroll = 0
		return
	}
	m.selected = len(m.rows) - 1
	m.syncFocus()
}

func (m *trackerModel) resetConfiguredState() {
	m.uiFilter = ""
	if m.detailFilters == nil {
		m.detailFilters = map[detailPane]string{}
	}
	m.detailFilters[detailPaneRequest] = ""
	m.detailFilters[detailPaneResult] = ""
	m.filterInputActive = false
	m.pendingG = false
	m.copyStatus = ""
	m.resetDetailScrolls()
	if m.DetailMode {
		m.syncRows()
		return
	}
	m.syncRowsToBottom()
}

func (m trackerModel) activeFilter() string {
	if m.DetailMode {
		return m.detailFilterForActivePane()
	}
	return m.uiFilter
}

func (m trackerModel) detailFilterForActivePane() string {
	return m.detailFilterForPane(m.normalizedDetailPane())
}

func (m trackerModel) detailFilterForPane(pane detailPane) string {
	if m.detailFilters == nil {
		return ""
	}
	return m.detailFilters[pane]
}

func (m *trackerModel) setDetailFilterForPane(pane detailPane, value string) {
	if m.detailFilters == nil {
		m.detailFilters = map[detailPane]string{}
	}
	m.detailFilters[pane] = value
	m.clampDetailScrolls()
	m.syncDetailScrollMirror()
}

func (m *trackerModel) appendActiveFilter(value string) {
	if m.DetailMode {
		pane := m.normalizedDetailPane()
		m.setDetailFilterForPane(pane, m.detailFilterForPane(pane)+value)
		return
	}
	m.uiFilter += value
	m.syncRows()
}

func (m *trackerModel) backspaceActiveFilter() {
	current := m.activeFilter()
	if current == "" {
		return
	}
	runes := []rune(current)
	next := string(runes[:len(runes)-1])
	if m.DetailMode {
		m.setDetailFilterForPane(m.normalizedDetailPane(), next)
		return
	}
	m.uiFilter = next
	m.syncRows()
}

func (m trackerModel) filteredRows() []requestRow {
	rows := m.store.Rows()
	filter := strings.ToLower(strings.TrimSpace(m.uiFilter))
	if filter == "" {
		return rows
	}
	out := make([]requestRow, 0, len(rows))
	for _, row := range rows {
		if row.matchesFilter(filter) {
			out = append(out, row)
		}
	}
	return out
}

func (r requestRow) matchesFilter(filter string) bool {
	if filter == "" {
		return true
	}
	values := []string{
		r.Method,
		r.URL,
		fmt.Sprint(r.Status),
		r.ContentType,
		r.ErrorText,
		r.BodyError,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), filter) {
			return true
		}
	}
	return false
}

func (m *trackerModel) syncFocus() {
	if len(m.rows) == 0 {
		m.focusedKey = ""
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	m.focusedKey = m.rows[m.selected].key()
	m.syncScroll()
}

func (m *trackerModel) moveToTop() {
	if len(m.rows) == 0 {
		return
	}
	m.selected = 0
	m.syncFocus()
}

func (m *trackerModel) moveToBottom() {
	if len(m.rows) == 0 {
		return
	}
	m.selected = len(m.rows) - 1
	m.syncFocus()
}

func (m *trackerModel) moveToNextError() {
	if len(m.rows) == 0 {
		return
	}
	for offset := 1; offset <= len(m.rows); offset++ {
		next := (m.selected + offset) % len(m.rows)
		if m.rows[next].isError() {
			m.selected = next
			m.syncFocus()
			return
		}
	}
}

func (m *trackerModel) scrollDetail(delta int) {
	pane := m.normalizedDetailPane()
	switch pane {
	case detailPaneResult:
		m.detailRightScroll = clampScroll(m.detailRightScroll+delta, m.maxDetailPaneScroll(detailPaneResult))
	default:
		m.detailLeftScroll = clampScroll(m.detailLeftScroll+delta, m.maxDetailPaneScroll(detailPaneRequest))
	}
	m.syncDetailScrollMirror()
}

func (m trackerModel) maxDetailScroll() int {
	return m.maxDetailPaneScroll(m.normalizedDetailPane())
}

func (m trackerModel) maxDetailPaneScroll(pane detailPane) int {
	left, right := m.filteredDetailColumns()
	lineCount := len(left)
	if pane == detailPaneResult {
		lineCount = len(right)
	}
	maxScroll := lineCount - m.effectiveDetailPaneContentHeight(pane)
	if maxScroll < 0 {
		return 0
	}
	return maxScroll
}

func (m trackerModel) effectiveScrollableDetailPane() detailPane {
	return m.normalizedDetailPane()
}

func (m *trackerModel) scrollDetailToTop() {
	switch m.normalizedDetailPane() {
	case detailPaneResult:
		m.detailRightScroll = 0
	default:
		m.detailLeftScroll = 0
	}
	m.syncDetailScrollMirror()
}

func (m *trackerModel) scrollDetailToBottom() {
	pane := m.normalizedDetailPane()
	switch pane {
	case detailPaneResult:
		m.detailRightScroll = m.maxDetailPaneScroll(detailPaneResult)
	default:
		m.detailLeftScroll = m.maxDetailPaneScroll(detailPaneRequest)
	}
	m.syncDetailScrollMirror()
}

func (m *trackerModel) resetDetailScrolls() {
	m.detailScroll = 0
	m.detailLeftScroll = 0
	m.detailRightScroll = 0
}

func (m *trackerModel) clampDetailScrolls() {
	m.detailLeftScroll = clampScroll(m.detailLeftScroll, m.maxDetailPaneScroll(detailPaneRequest))
	m.detailRightScroll = clampScroll(m.detailRightScroll, m.maxDetailPaneScroll(detailPaneResult))
}

func (m *trackerModel) syncDetailScrollMirror() {
	if m.normalizedDetailPane() == detailPaneResult {
		m.detailScroll = m.detailRightScroll
		return
	}
	m.detailScroll = m.detailLeftScroll
}

func clampScroll(value, max int) int {
	if value < 0 {
		return 0
	}
	if value > max {
		return max
	}
	return value
}

func (m *trackerModel) syncScroll() {
	if len(m.rows) == 0 {
		m.scroll = 0
		return
	}
	height := m.effectiveListHeight()
	if m.selected < m.scroll {
		m.scroll = m.selected
	}
	if m.selected >= m.scroll+height {
		m.scroll = m.selected - height + 1
	}
	maxScroll := len(m.rows) - height
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m trackerModel) effectiveListHeight() int {
	if m.listHeight > 0 {
		return m.listHeight
	}
	return 15
}

func listHeightForWindow(height int) int {
	const headerLines = 7
	listHeight := height - headerLines
	if listHeight < 3 {
		return 3
	}
	return listHeight
}

func (m trackerModel) visibleRows() []requestRow {
	if len(m.rows) == 0 {
		return nil
	}
	height := m.effectiveListHeight()
	start := m.scroll
	if start > len(m.rows) {
		start = len(m.rows)
	}
	end := start + height
	if end > len(m.rows) {
		end = len(m.rows)
	}
	out := make([]requestRow, end-start)
	copy(out, m.rows[start:end])
	return out
}

func (m *trackerModel) applyTabEvent(event tabEvent) {
	switch event.Kind {
	case tabCreated:
		m.tabs[event.Target.ID] = event.Target
	case tabClosed:
		delete(m.tabs, event.TargetID)
	case tabNavigated:
		targetID := event.TargetID
		if targetID == "" {
			targetID = event.Target.ID
		}
		target := m.tabs[targetID]
		target.ID = targetID
		if event.URL != "" {
			target.URL = event.URL
		}
		m.tabs[targetID] = target
	}
}

func (m trackerModel) View() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	fmt.Fprintf(&b, "%s\n", titleStyle.Render("TAB NETWORK"))
	mode := "launch"
	if !m.config.LaunchChrome {
		mode = "attach"
	}
	fmt.Fprintf(&b, "mode=%s port=%d target-id=%s requests=%d", mode, m.config.Port, valueOrDash(m.config.TargetID), len(m.rows))
	if m.chromePID != 0 {
		fmt.Fprintf(&b, " chrome-pid=%d", m.chromePID)
	}
	b.WriteString("\n")
	if m.config.Filter.URLContains != "" || m.config.Filter.Method != "" {
		fmt.Fprintf(&b, "filter-url=%q method=%q\n", m.config.Filter.URLContains, m.config.Filter.Method)
	}
	activeFilter := m.activeFilter()
	if m.filterInputActive || activeFilter != "" {
		prompt := m.activeFilterLabel()
		if m.filterInputActive {
			prompt += ">"
		}
		fmt.Fprintf(&b, "%s %q\n", prompt, activeFilter)
	}
	fmt.Fprintf(&b, "current-url=%s\n", valueOrDash(m.currentURL()))
	if m.lastError != "" {
		fmt.Fprintf(&b, "%s\n", errorStyle.Render(m.lastError))
	}
	if m.copyStatus != "" {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(m.copyStatus))
	}
	b.WriteString(mutedStyle.Render("j/k or arrows: move/scroll  c: copy focused pane  e: next error  f: filter  1/2: detail pane  l/enter: detail  h/esc: list  gg/G: top/bottom  q: quit"))
	b.WriteString("\n\n")

	if m.DetailMode {
		return m.fillScreen(b.String() + m.detailView())
	}
	return m.fillScreen(b.String() + m.listView())
}

func (m trackerModel) currentURL() string {
	var ids []string
	for id, target := range m.tabs {
		if target.URL != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return m.tabs[ids[0]].URL
}

func (m trackerModel) activeFilterLabel() string {
	if !m.DetailMode {
		return "list-filter"
	}
	return fmt.Sprintf("detail-filter[%d]", m.normalizedDetailPane())
}

func (m trackerModel) listView() string {
	if len(m.rows) == 0 {
		return "Waiting for network requests...\n"
	}

	var b strings.Builder
	focusedStyle := lipgloss.NewStyle().Bold(true).Reverse(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	focusedErrorStyle := lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(lipgloss.Color("196"))
	visibleRows := m.visibleRows()
	for visibleIndex, row := range visibleRows {
		i := m.scroll + visibleIndex
		cursor := " "
		if i == m.selected {
			cursor = ">"
		}
		status := "-"
		if row.Status != 0 {
			status = fmt.Sprintf("%d", row.Status)
		}
		marker := "   "
		if row.isError() {
			marker = "ERR"
		}
		line := fmt.Sprintf("%s %-3s %-6s %-4s x%-3d %-12s %s",
			cursor,
			marker,
			row.Method,
			status,
			row.Count,
			row.LastSeen.Format("15:04:05.000"),
			shorten(row.URL, 84),
		)
		if i == m.selected && row.isError() {
			line = focusedErrorStyle.Render(line)
		} else if i == m.selected {
			line = focusedStyle.Render(line)
		} else if row.isError() {
			line = errorStyle.Render(line)
		}
		fmt.Fprintln(&b, line)
	}
	if len(m.rows) > m.effectiveListHeight() {
		fmt.Fprintf(&b, "showing %d-%d of %d\n", m.scroll+1, m.scroll+len(visibleRows), len(m.rows))
	}
	return b.String()
}

func (m trackerModel) detailView() string {
	lines := m.visibleDetailLines()
	return m.detailScrollIndicator() + "\n" + strings.Join(lines, "\n") + "\n"
}

func (m trackerModel) detailScrollIndicator() string {
	pane := m.effectiveScrollableDetailPane()
	scroll := m.detailLeftScroll
	if pane == detailPaneResult {
		scroll = m.detailRightScroll
	}
	total := len(m.detailPaneLines(pane))
	if total == 0 {
		return "detail-scroll 0-0 of 0"
	}
	contentHeight := m.effectiveDetailPaneContentHeight(pane)
	start := scroll + 1
	end := scroll + contentHeight
	if end > total {
		end = total
	}
	return fmt.Sprintf(
		"detail-scroll %d-%d of %d  active=%d request-scroll=%d result-scroll=%d",
		start,
		end,
		total,
		m.normalizedDetailPane(),
		m.detailLeftScroll,
		m.detailRightScroll,
	)
}

func (m trackerModel) visibleDetailLines() []string {
	height := m.effectiveDetailHeight()
	if height <= 0 {
		return nil
	}
	left, right := m.filteredDetailColumns()

	width := m.effectiveWidth()
	leftWidth := (width - 3) / 2
	if leftWidth < 30 {
		leftWidth = 30
	}
	rightWidth := width - leftWidth - 3
	if rightWidth < 30 {
		rightWidth = 30
	}
	left = sliceLines(left, m.detailLeftScroll, m.effectiveDetailPaneContentHeight(detailPaneRequest))
	right = sliceLines(right, m.detailRightScroll, m.effectiveDetailPaneContentHeight(detailPaneResult))
	left = m.decorateDetailPane(detailPaneRequest, left, leftWidth)
	right = m.decorateDetailPane(detailPaneResult, right, rightWidth)
	rendered := strings.TrimRight(joinColumns(left, right, leftWidth, rightWidth), "\n")
	if rendered == "" {
		return nil
	}
	return strings.Split(rendered, "\n")
}

func (m trackerModel) effectiveDetailHeight() int {
	if m.listHeight > 0 {
		return m.listHeight
	}
	if m.height > 0 {
		return listHeightForWindow(m.height)
	}
	return 15
}

func (m trackerModel) effectiveDetailPaneContentHeight(pane detailPane) int {
	height := m.effectiveDetailHeight()
	if m.normalizedDetailPane() == pane {
		height -= 2
	}
	if height < 1 {
		return 1
	}
	return height
}

func (m trackerModel) fullDetailLines() []string {
	left, right := m.filteredDetailColumns()
	width := m.effectiveWidth()
	leftWidth := (width - 3) / 2
	if leftWidth < 30 {
		leftWidth = 30
	}
	rightWidth := width - leftWidth - 3
	if rightWidth < 30 {
		rightWidth = 30
	}

	rendered := strings.TrimRight(joinColumns(left, right, leftWidth, rightWidth), "\n")
	if rendered == "" {
		return nil
	}
	return strings.Split(rendered, "\n")
}

func (m trackerModel) detailColumns() ([]string, []string) {
	if len(m.rows) == 0 {
		return []string{"[1] REQUEST HEADERS", "", "No request selected."}, []string{"[2] CALL RESULT", "", "No request selected."}
	}
	row := m.rows[m.selected]
	status := "-"
	if row.Status != 0 {
		status = fmt.Sprintf("%d", row.Status)
	}

	left := []string{
		m.detailTitle(detailPaneRequest, "REQUEST HEADERS"),
		"",
		"Method: " + row.Method,
		"URL: " + row.URL,
		"Request ID: " + valueOrDash(row.LastRequestID),
		"Target ID: " + valueOrDash(row.TargetID),
		"",
	}
	left = append(left, formatHeaders(row.RequestHeaders)...)

	right := []string{
		m.detailTitle(detailPaneResult, "CALL RESULT"),
		"",
		"Status: " + status,
		"Content-Type: " + valueOrDash(row.ContentType),
		"Count: " + fmt.Sprint(row.Count),
		"First Seen: " + row.FirstSeen.Format(time.RFC3339),
		"Last Seen: " + row.LastSeen.Format(time.RFC3339),
		"Error: " + valueOrDash(row.ErrorText),
		"Body Error: " + valueOrDash(row.BodyError),
		"",
		"Response Headers",
	}
	right = append(right, formatHeaders(row.ResponseHeaders)...)
	right = append(right, "", "Response Body")
	right = append(right, strings.Split(formatResponseBody(valueOrDash(row.ResponseBody)), "\n")...)

	return left, right
}

func (m trackerModel) filteredDetailColumns() ([]string, []string) {
	left, right := m.detailColumns()
	return filterDetailLines(left, m.detailFilterForPane(detailPaneRequest)),
		filterDetailLines(right, m.detailFilterForPane(detailPaneResult))
}

func filterDetailLines(lines []string, filter string) []string {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return lines
	}
	out := make([]string, 0, len(lines))
	if len(lines) > 0 {
		out = append(out, lines[0])
	}
	for _, line := range lines[1:] {
		if strings.Contains(strings.ToLower(line), filter) {
			out = append(out, line)
		}
	}
	if len(out) == 1 {
		out = append(out, "", "No matching detail lines.")
	}
	return out
}

func (m trackerModel) detailTitle(pane detailPane, title string) string {
	prefix := fmt.Sprintf("[%d] ", pane)
	if m.normalizedDetailPane() == pane {
		return prefix + title + " *"
	}
	return prefix + title
}

func (m trackerModel) normalizedDetailPane() detailPane {
	if m.detailPane == detailPaneResult {
		return detailPaneResult
	}
	return detailPaneRequest
}

func (m trackerModel) detailPaneLines(pane detailPane) []string {
	left, right := m.filteredDetailColumns()
	if pane == detailPaneResult {
		return right
	}
	return left
}

func (m trackerModel) focusedDetailCopyText() (string, string) {
	if len(m.rows) == 0 {
		return "", ""
	}
	left, right := m.filteredDetailColumns()
	if m.normalizedDetailPane() == detailPaneResult {
		return strings.Join(right, "\n"), "result"
	}
	return strings.Join(left, "\n"), "request"
}

func (m trackerModel) decorateDetailPane(pane detailPane, lines []string, width int) []string {
	if m.normalizedDetailPane() != pane {
		return lines
	}
	return borderedLines(lines, width)
}

func borderedLines(lines []string, width int) []string {
	if width < 2 {
		return lines
	}
	innerWidth := width - 2
	out := make([]string, 0, len(lines)+2)
	out = append(out, "╭"+strings.Repeat("─", innerWidth)+"╮")
	for _, line := range lines {
		out = append(out, "│"+padToWidth(shorten(line, innerWidth), innerWidth)+"│")
	}
	out = append(out, "╰"+strings.Repeat("─", innerWidth)+"╯")
	return out
}

func sliceLines(lines []string, start, height int) []string {
	if height <= 0 {
		return nil
	}
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]string, end-start)
	copy(out, lines[start:end])
	return out
}

func (m trackerModel) fillScreen(content string) string {
	if m.height <= 0 {
		return content
	}
	width := m.effectiveWidth()
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for i, line := range lines {
		if plainLen(line) < width {
			lines[i] = line + strings.Repeat(" ", width-plainLen(line))
		}
	}
	for len(lines) < m.height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m trackerModel) effectiveWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 100
}

func formatHeaders(headers map[string]string) []string {
	if len(headers) == 0 {
		return []string{"-"}
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+": "+headers[key])
	}
	return lines
}

func joinColumns(left, right []string, leftWidth, rightWidth int) string {
	left = wrapColumnLines(left, leftWidth)
	right = wrapColumnLines(right, rightWidth)
	lineCount := len(left)
	if len(right) > lineCount {
		lineCount = len(right)
	}
	var b strings.Builder
	for i := 0; i < lineCount; i++ {
		leftLine := ""
		if i < len(left) {
			leftLine = left[i]
		}
		rightLine := ""
		if i < len(right) {
			rightLine = right[i]
		}
		fmt.Fprintf(&b, "%s │ %s\n", padToWidth(leftLine, leftWidth), padToWidth(rightLine, rightWidth))
	}
	return b.String()
}

func wrapColumnLines(lines []string, width int) []string {
	if width <= 0 {
		return nil
	}
	var out []string
	for _, line := range lines {
		if line == "" {
			out = append(out, "")
			continue
		}
		for plainLen(line) > width {
			head, tail := splitAtRunes(line, width)
			out = append(out, head)
			line = tail
		}
		out = append(out, line)
	}
	return out
}

func padToWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return value + strings.Repeat(" ", width-plainLen(value))
}

func plainLen(value string) int {
	return len([]rune(value))
}

func formatResponseBody(value string) string {
	if value == "" || value == "-" {
		return valueOrDash(value)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(value), "", "  "); err == nil {
		return buf.String()
	}
	return value
}

func shorten(value string, max int) string {
	runes := []rune(value)
	if max <= 3 || len(runes) <= max {
		return value
	}
	return string(runes[:max-3]) + "..."
}

func splitAtRunes(value string, width int) (string, string) {
	if width <= 0 {
		return "", value
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value, ""
	}
	return string(runes[:width]), string(runes[width:])
}

type clipboardEvent struct {
	Err error
}

func copyToClipboard(value string) tea.Cmd {
	return func() tea.Msg {
		return clipboardEvent{Err: writeClipboard(value)}
	}
}

func writeClipboard(value string) error {
	if value == "" {
		return nil
	}
	commands := []struct {
		name string
		args []string
	}{
		{name: "pbcopy"},
		{name: "wl-copy"},
		{name: "xclip", args: []string{"-selection", "clipboard"}},
		{name: "xsel", args: []string{"--clipboard", "--input"}},
	}
	for _, candidate := range commands {
		path, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, candidate.args...)
		cmd.Stdin = strings.NewReader(value)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	if err := writeOSC52ToTTY(value); err == nil {
		return nil
	}
	return errors.New("no clipboard command or tty OSC52 fallback available")
}

func osc52ClipboardSequence(value string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(value)) + "\a"
}

func writeOSC52ToTTY(value string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	_, err = tty.WriteString(osc52ClipboardSequence(value))
	return err
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
