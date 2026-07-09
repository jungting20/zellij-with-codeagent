package tabwatcher

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	if err := runWatcher(ctx, opts, stdout, stderr, client, chromedpTargetSource{ctx: browserCtx}); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
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
	go func() {
		_ = cmd.Wait()
	}()
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

func shortTargetID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
