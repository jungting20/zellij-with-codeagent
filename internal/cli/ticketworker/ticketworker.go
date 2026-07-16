package ticketworkercli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/planner"
	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

type Client interface {
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
}

type Manager interface {
	Run(context.Context) error
}

type Config struct {
	Executable []string
	NewClient  func(string, time.Duration) Client
	NewManager func(ticketworker.ManagerOptions) (Manager, error)
	Getwd      func() (string, error)
	Now        func() time.Time
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg Config) int {
	_ = stdin
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	if len(args) == 1 && isHelp(args[0]) {
		printUsage(stdout)
		return 0
	}

	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr, cfg)
	case "start":
		return runStart(args[1:], stdout, stderr, cfg)
	case "manager":
		return runManager(args[1:], stdout, stderr, cfg)
	default:
		fmt.Fprintf(stderr, "unknown ticket-worker command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runInit(args []string, stdout, stderr io.Writer, cfg Config) int {
	fs := flag.NewFlagSet("ticket-worker init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "replace an existing ticket-worker config")
	cwdFlag := fs.String("cwd", "", "project root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "ticket-worker init does not accept positional arguments")
		return 2
	}
	cwd, err := resolveCWD(*cwdFlag, cfg.Getwd)
	if err != nil {
		fmt.Fprintf(stderr, "resolve project root: %v\n", err)
		return 1
	}
	path, err := ticketworker.InitConfig(cwd, *force)
	if err != nil {
		fmt.Fprintf(stderr, "initialize ticket-worker config: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "initialized ticket-worker config: %s\n", path)
	return 0
}

func runStart(args []string, stdout, stderr io.Writer, cfg Config) int {
	fs := flag.NewFlagSet("ticket-worker start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 15*time.Second, "request timeout")
	cwdFlag := fs.String("cwd", "", "project root")
	configFlag := fs.String("config", "", "ticket-worker config path")
	sessionFlag := fs.String("session", "", "execution session/task id override")
	maxWorkers := fs.Int("max-workers", 0, "override configured worker capacity")
	dryRun := fs.Bool("dry-run", false, "print the /v1/requests envelope without submitting it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "ticket-worker start does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "ticket-worker start --timeout must be positive")
		return 2
	}

	cwd, err := resolveCWD(*cwdFlag, cfg.Getwd)
	if err != nil {
		fmt.Fprintf(stderr, "resolve project root: %v\n", err)
		return 1
	}
	configPath, workerConfig, err := loadConfig(cwd, *configFlag)
	if err != nil {
		fmt.Fprintf(stderr, "load ticket-worker config: %v\n", err)
		return 1
	}
	maxWorkersSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "max-workers" {
			maxWorkersSet = true
		}
	})
	if maxWorkersSet {
		workerConfig.MaxWorkers = *maxWorkers
	}

	session := strings.TrimSpace(*sessionFlag)
	if session == "" {
		now := time.Now
		if cfg.Now != nil {
			now = cfg.Now
		}
		session = ticketworker.SessionID(cwd, now())
	}
	executable := cfg.Executable
	if len(executable) == 0 {
		executable = []string{"zellij-agent"}
	}
	payload, err := ticketworker.BuildPlan(ticketworker.PlanRequest{
		CWD: cwd, ConfigPath: configPath, Session: session,
		Executable: executable, SocketPath: nonDefaultSocket(*socketPath), Config: workerConfig,
	})
	if err != nil {
		fmt.Fprintf(stderr, "build ticket-worker plan: %v\n", err)
		return 1
	}
	if maxWorkersSet {
		managerCommand := &payload.Tabs[0].Panes[0].Command
		*managerCommand = append(*managerCommand, "--max-workers", strconv.Itoa(workerConfig.MaxWorkers))
	}
	requestID := ticketworker.RequestID(payload.Session)
	envelope, err := executionPlanEnvelope(requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "encode ticket-worker plan: %v\n", err)
		return 1
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		fmt.Fprintf(stderr, "encode ticket-worker plan: %v\n", err)
		return 1
	}
	if _, err := planner.ParseExecutionPlanEnvelope(raw); err != nil {
		fmt.Fprintf(stderr, "validate generated ticket-worker plan: %v\n", err)
		return 1
	}
	if *dryRun {
		if err := writeEnvelope(stdout, envelope); err != nil {
			fmt.Fprintf(stderr, "write ticket-worker dry-run: %v\n", err)
			return 1
		}
		return 0
	}

	newClient := cfg.NewClient
	if newClient == nil {
		newClient = func(socketPath string, timeout time.Duration) Client {
			return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout, AutoStart: true})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	response, err := newClient(*socketPath, *timeout).SubmitExecutionPlan(ctx, requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "ticket-worker submit failed via socket %s: %v\n", *socketPath, err)
		return 1
	}
	printExecutionPlanResponse(stdout, response)
	return 0
}

func runManager(args []string, stdout, stderr io.Writer, cfg Config) int {
	fs := flag.NewFlagSet("ticket-worker manager", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 15*time.Second, "request timeout")
	cwdFlag := fs.String("cwd", "", "project root")
	configFlag := fs.String("config", "", "ticket-worker config path")
	taskID := fs.String("task", "", "ticket-worker task/session id")
	anchor := fs.String("anchor", "", "manager logical pane id")
	maxWorkers := fs.Int("max-workers", 0, "override configured worker capacity")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "ticket-worker manager does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "ticket-worker manager --timeout must be positive")
		return 2
	}
	cwd, err := resolveCWD(*cwdFlag, cfg.Getwd)
	if err != nil {
		fmt.Fprintf(stderr, "resolve manager project root: %v\n", err)
		return 1
	}
	_, workerConfig, err := loadConfig(cwd, *configFlag)
	if err != nil {
		fmt.Fprintf(stderr, "load ticket-worker config: %v\n", err)
		return 1
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "max-workers" {
			workerConfig.MaxWorkers = *maxWorkers
		}
	})

	client := transport.NewClient(transport.ClientOptions{
		SocketPath: *socketPath,
		Timeout:    *timeout,
		AutoStart:  false,
	})
	newManager := cfg.NewManager
	if newManager == nil {
		newManager = func(opts ticketworker.ManagerOptions) (Manager, error) {
			return ticketworker.NewManager(opts)
		}
	}
	manager, err := newManager(ticketworker.ManagerOptions{
		Client: client, Config: workerConfig,
		TaskID: *taskID, AnchorPaneID: *anchor, CWD: cwd, Log: stdout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "create ticket-worker manager: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := manager.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "ticket-worker manager failed: %v\n", err)
		return 1
	}
	return 0
}

func resolveCWD(value string, getwd func() (string, error)) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if getwd == nil {
			getwd = os.Getwd
		}
		var err error
		value, err = getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

func loadConfig(cwd, configValue string) (string, ticketworker.Config, error) {
	configPath := strings.TrimSpace(configValue)
	if configPath == "" {
		configPath = ticketworker.ConfigPath(cwd)
	} else {
		var err error
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return "", ticketworker.Config{}, err
		}
	}
	const suffix = "/.zellij-agent/worker/config.yaml"
	if !strings.HasSuffix(filepath.ToSlash(configPath), suffix) {
		return "", ticketworker.Config{}, fmt.Errorf("config path must end in %s", suffix)
	}
	root := strings.TrimSuffix(filepath.ToSlash(configPath), suffix)
	if root == "" {
		root = string(filepath.Separator)
	}
	workerConfig, err := ticketworker.LoadConfig(filepath.FromSlash(root))
	if err != nil {
		return "", ticketworker.Config{}, err
	}
	return configPath, workerConfig, nil
}

func executionPlanEnvelope(requestID string, payload transport.ExecutionPlanPayload) (transport.RequestEnvelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return transport.RequestEnvelope{}, err
	}
	return transport.RequestEnvelope{Type: transport.RequestTypeExecutionPlan, RequestID: requestID, Payload: raw}, nil
}

func writeEnvelope(w io.Writer, envelope transport.RequestEnvelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

func printExecutionPlanResponse(w io.Writer, response transport.ExecutionPlanResponse) {
	paneCount := 0
	for _, tab := range response.Tabs {
		paneCount += len(tab.Panes)
	}
	fmt.Fprintf(w, "request=%s session=%s layout=%s tabs=%d panes=%d\n", response.RequestID, response.Session, response.Layout, len(response.Tabs), paneCount)
}

func nonDefaultSocket(socketPath string) string {
	if socketPath == cli.DefaultSocketPath {
		return ""
	}
	return socketPath
}

func isHelp(value string) bool {
	return value == "help" || value == "-h" || value == "--help"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent ticket-worker <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init    Create the project ticket-worker config")
	fmt.Fprintln(w, "  start   Launch the manager and read-only monitor workspace")
}
