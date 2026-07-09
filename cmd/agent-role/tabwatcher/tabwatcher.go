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

func shortTargetID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
