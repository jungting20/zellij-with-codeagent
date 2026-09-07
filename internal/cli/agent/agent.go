package agentcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/agentdashboard"
	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/transport"
)

const (
	defaultTimeout         = 10 * time.Second
	defaultRefreshInterval = 2 * time.Second
)

type AgentClient interface {
	Health(context.Context) (transport.HealthResponse, error)
	StartAgent(context.Context, transport.StartAgentRequest) (transport.StartAgentResponse, error)
	ClosePane(context.Context, string) (transport.ClosePaneResponse, error)
	FocusNextAgent(context.Context, transport.FocusNextAgentRequest) (transport.FocusNextAgentResponse, error)
	FocusPreviousAgent(context.Context, transport.FocusPreviousAgentRequest) (transport.FocusPreviousAgentResponse, error)
	agentdashboard.Client
}

type ClientFactory func(socketPath string, timeout time.Duration) AgentClient
type AgentRunner func(command []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) error

type Config struct {
	Getwd      func() (string, error)
	Getenv     func(string) string
	NewModel   func(context.Context, agentdashboard.Client, agentdashboard.Options) tea.Model
	RunProgram func(context.Context, tea.Model, io.Reader, io.Writer) error
	RunAgent   AgentRunner
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "start":
		return runStart(args[1:], stdin, stdout, stderr, newClient, cfg)
	case "next":
		return runNext(args[1:], stdout, stderr, newClient, cfg)
	case "prev":
		return runPrevious(args[1:], stdout, stderr, newClient, cfg)
	case "dashboard":
		return runDashboard(args[1:], stdin, stdout, stderr, newClient, cfg)
	default:
		fmt.Fprintf(stderr, "unknown agent command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runNext(args []string, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	if len(args) == 1 && isHelp(args[0]) {
		printNextUsage(stdout)
		return 0
	}
	fs := flag.NewFlagSet("agent next", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", defaultTimeout, "request timeout")
	idleOnly := fs.Bool("idle-only", false, "cycle only through idle agents")
	pinnedOnly := fs.Bool("pinned-only", false, "cycle only through pinned agents")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "agent next does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "agent next --timeout must be positive")
		return 2
	}
	if cfg.Getenv == nil {
		fmt.Fprintln(stderr, "agent next configuration error: Getenv is required")
		return 1
	}
	session := strings.TrimSpace(cfg.Getenv("ZELLIJ_SESSION_NAME"))
	paneID := normalizeZellijPaneID(cfg.Getenv("ZELLIJ_PANE_ID"))
	if session == "" || paneID == "" {
		fmt.Fprintln(stderr, "agent next must run inside a Zellij pane (ZELLIJ_SESSION_NAME and ZELLIJ_PANE_ID are required)")
		return 2
	}
	if newClient == nil {
		fmt.Fprintln(stderr, "agent next client is not configured")
		return 1
	}
	client := newClient(*socket, *timeout)
	if isNilAgentClient(client) {
		fmt.Fprintln(stderr, "agent next client is not configured")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	response, err := client.FocusNextAgent(ctx, transport.FocusNextAgentRequest{
		SourceSession:      session,
		SourceZellijPaneID: paneID,
		IdleOnly:           *idleOnly,
		PinnedOnly:         *pinnedOnly,
	})
	if err != nil {
		fmt.Fprintf(stderr, "agent next failed via socket %s: %v\n", *socket, err)
		return 1
	}
	if !response.Focused {
		return 0
	}

	focusedPaneID := response.Agent.Agent.PaneID
	if focusedPaneID == "" {
		focusedPaneID = response.Agent.Pane.ID
	}
	fmt.Fprintf(stdout, "focused agent=%s kind=%s pane=%s\n", response.Agent.Agent.ID, response.Agent.Agent.Kind, focusedPaneID)
	return 0
}

func runPrevious(args []string, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	if len(args) == 1 && isHelp(args[0]) {
		printPreviousUsage(stdout)
		return 0
	}
	fs := flag.NewFlagSet("agent prev", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", defaultTimeout, "request timeout")
	idleOnly := fs.Bool("idle-only", false, "cycle only through idle agents")
	pinnedOnly := fs.Bool("pinned-only", false, "cycle only through pinned agents")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "agent prev does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "agent prev --timeout must be positive")
		return 2
	}
	if cfg.Getenv == nil {
		fmt.Fprintln(stderr, "agent prev configuration error: Getenv is required")
		return 1
	}
	session := strings.TrimSpace(cfg.Getenv("ZELLIJ_SESSION_NAME"))
	paneID := normalizeZellijPaneID(cfg.Getenv("ZELLIJ_PANE_ID"))
	if session == "" || paneID == "" {
		fmt.Fprintln(stderr, "agent prev must run inside a Zellij pane (ZELLIJ_SESSION_NAME and ZELLIJ_PANE_ID are required)")
		return 2
	}
	if newClient == nil {
		fmt.Fprintln(stderr, "agent prev client is not configured")
		return 1
	}
	client := newClient(*socket, *timeout)
	if isNilAgentClient(client) {
		fmt.Fprintln(stderr, "agent prev client is not configured")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	response, err := client.FocusPreviousAgent(ctx, transport.FocusPreviousAgentRequest{
		SourceSession:      session,
		SourceZellijPaneID: paneID,
		IdleOnly:           *idleOnly,
		PinnedOnly:         *pinnedOnly,
	})
	if err != nil {
		fmt.Fprintf(stderr, "agent prev failed via socket %s: %v\n", *socket, err)
		return 1
	}
	if !response.Focused {
		return 0
	}

	focusedPaneID := response.Agent.Agent.PaneID
	if focusedPaneID == "" {
		focusedPaneID = response.Agent.Pane.ID
	}
	fmt.Fprintf(stdout, "focused agent=%s kind=%s pane=%s\n", response.Agent.Agent.ID, response.Agent.Agent.Kind, focusedPaneID)
	return 0
}

func runDashboard(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	if len(args) == 1 && isHelp(args[0]) {
		printDashboardUsage(stdout)
		return 0
	}
	fs := flag.NewFlagSet("agent dashboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", defaultTimeout, "request timeout")
	refresh := fs.Duration("refresh-interval", defaultRefreshInterval, "polling refresh interval")
	output := fs.String("output", "tui", "output mode: tui or json")
	focusAgentID := fs.String("focus", "", "focus one agent and exit (requires --output json)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "agent dashboard does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "agent dashboard --timeout must be positive")
		return 2
	}
	if *refresh <= 0 {
		fmt.Fprintln(stderr, "agent dashboard --refresh-interval must be positive")
		return 2
	}
	if *output != "tui" && *output != "json" {
		fmt.Fprintln(stderr, "agent dashboard --output must be tui or json")
		return 2
	}
	if strings.TrimSpace(*focusAgentID) != "" && *output != "json" {
		fmt.Fprintln(stderr, "agent dashboard --focus requires --output json")
		return 2
	}
	if newClient == nil {
		fmt.Fprintln(stderr, "agent dashboard client is not configured")
		return 1
	}
	client := newClient(*socket, *timeout)
	if isNilAgentClient(client) {
		fmt.Fprintln(stderr, "agent dashboard client is not configured")
		return 1
	}
	if *output == "json" && strings.TrimSpace(*focusAgentID) == "" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		response, err := client.ListAgents(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "agent dashboard list failed via socket %s: %v\n", *socket, err)
			return 1
		}
		if err := json.NewEncoder(stdout).Encode(response); err != nil {
			fmt.Fprintf(stderr, "agent dashboard encode failed: %v\n", err)
			return 1
		}
		return 0
	}
	if cfg.Getenv == nil {
		fmt.Fprintln(stderr, "agent dashboard configuration error: Getenv is required")
		return 1
	}
	session := strings.TrimSpace(cfg.Getenv("ZELLIJ_SESSION_NAME"))
	paneID := normalizeZellijPaneID(cfg.Getenv("ZELLIJ_PANE_ID"))
	if session == "" || paneID == "" {
		fmt.Fprintln(stderr, "agent dashboard must run inside a Zellij pane (ZELLIJ_SESSION_NAME and ZELLIJ_PANE_ID are required)")
		return 2
	}
	if strings.TrimSpace(*focusAgentID) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		response, err := client.FocusAgent(ctx, strings.TrimSpace(*focusAgentID), transport.FocusAgentRequest{
			SourceSession:      session,
			SourceZellijPaneID: paneID,
		})
		if err != nil {
			fmt.Fprintf(stderr, "agent dashboard focus failed via socket %s: %v\n", *socket, err)
			return 1
		}
		if err := json.NewEncoder(stdout).Encode(response); err != nil {
			fmt.Fprintf(stderr, "agent dashboard encode failed: %v\n", err)
			return 1
		}
		return 0
	}
	newModel := cfg.NewModel
	if newModel == nil {
		newModel = agentdashboard.NewModel
	}
	runner := cfg.RunProgram
	if runner == nil {
		runner = runProgram
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	model := newModel(ctx, client, agentdashboard.Options{
		RefreshInterval:    *refresh,
		SourceSession:      session,
		SourceZellijPaneID: paneID,
	})
	if err := runner(ctx, model, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "agent dashboard failed: %v\n", err)
		return 1
	}
	return 0
}

func runProgram(ctx context.Context, model tea.Model, stdin io.Reader, stdout io.Writer) error {
	_, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithAltScreen()).Run()
	return err
}

func runStart(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	if len(args) == 1 && isHelp(args[0]) {
		printStartUsage(stdout)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "start requires <codex|claude|gemini|cursor|hermes>")
		return 2
	}

	kind := args[0]
	if !supportedKind(kind) {
		fmt.Fprintf(stderr, "unsupported agent kind: %s (want codex, claude, gemini, cursor, or hermes)\n", kind)
		return 2
	}

	before, extra := splitPassthrough(args[1:])
	opts, err := parseStartOptions(before)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if opts.timeout <= 0 {
		fmt.Fprintln(stderr, "--timeout must be greater than 0")
		return 2
	}
	if opts.access == string(codingagent.AccessReadOnly) && kind != string(codingagent.KindCodex) {
		fmt.Fprintln(stderr, "read-only access is supported only by Codex")
		return 2
	}
	var prompt string
	if opts.access == string(codingagent.AccessReadOnly) {
		if len(extra) > 1 {
			fmt.Fprintln(stderr, "read-only access accepts at most one prompt after --")
			return 2
		}
		if len(extra) == 1 {
			if strings.HasPrefix(extra[0], "-") {
				fmt.Fprintln(stderr, "read-only prompt must not begin with '-'")
				return 2
			}
			prompt = extra[0]
		}
	}
	if cfg.Getwd == nil {
		fmt.Fprintln(stderr, "agent start configuration error: Getwd is required")
		return 1
	}
	if cfg.Getenv == nil {
		fmt.Fprintln(stderr, "agent start configuration error: Getenv is required")
		return 1
	}

	cwd, err := resolveCWD(opts.cwd, cfg.Getwd)
	if err != nil {
		if opts.cwd == "" {
			fmt.Fprintf(stderr, "determine working directory: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "resolve cwd: %v\n", err)
		return 2
	}

	session := strings.TrimSpace(cfg.Getenv("ZELLIJ_SESSION_NAME"))
	if session == "" {
		fmt.Fprintln(stderr, "ZELLIJ_SESSION_NAME is required; agent start must run inside Zellij")
		return 2
	}
	paneID := normalizeZellijPaneID(cfg.Getenv("ZELLIJ_PANE_ID"))
	if paneID == "" {
		fmt.Fprintln(stderr, "ZELLIJ_PANE_ID is required; agent start must run inside a Zellij pane")
		return 2
	}
	if newClient == nil {
		fmt.Fprintln(stderr, "agent start client is not configured")
		return 1
	}
	client := newClient(opts.socket, opts.timeout)
	if isNilAgentClient(client) {
		fmt.Fprintln(stderr, "agent start client is not configured")
		return 1
	}
	if opts.access == string(codingagent.AccessReadOnly) {
		healthCtx, healthCancel := context.WithTimeout(context.Background(), opts.timeout)
		health, healthErr := client.Health(healthCtx)
		healthCancel()
		if healthErr != nil {
			fmt.Fprintf(stderr, "agent start capability check failed via socket %s: %v\n", opts.socket, healthErr)
			return 1
		}
		if !containsString(health.Capabilities, transport.CapabilityAgentAccessReadOnlyV1) {
			fmt.Fprintln(stderr, "installed CLI and running daemon differ; drain/restart daemon before read-only start")
			return 1
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	access := ""
	requestArgs := append([]string(nil), extra...)
	if opts.access == string(codingagent.AccessReadOnly) {
		access = opts.access
		requestArgs = nil
	}
	response, err := client.StartAgent(ctx, transport.StartAgentRequest{
		Kind:               kind,
		CWD:                cwd,
		Access:             access,
		Prompt:             prompt,
		Args:               requestArgs,
		NotifyOnIdle:       opts.notifyOnIdle,
		SourceSession:      session,
		SourceZellijPaneID: paneID,
	})
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "agent start failed via socket %s: %v\n", opts.socket, err)
		return 1
	}

	startedPaneID := response.Agent.Agent.PaneID
	if startedPaneID == "" {
		startedPaneID = response.Agent.Pane.ID
	}

	runner := cfg.RunAgent
	if runner == nil {
		runner = runAgentProcess
	}
	runErr := runner(response.Agent.Pane.Command, response.Agent.Pane.CWD, stdin, stdout, stderr)

	closeCtx, cancelClose := context.WithTimeout(context.Background(), opts.timeout)
	_, closeErr := client.ClosePane(closeCtx, startedPaneID)
	cancelClose()

	if runErr != nil {
		fmt.Fprintf(stderr, "coding agent process failed: %v\n", runErr)
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "close coding agent pane %s failed via socket %s: %v\n", startedPaneID, opts.socket, closeErr)
	}
	if runErr != nil || closeErr != nil {
		return 1
	}
	return 0
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func runAgentProcess(command []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return errors.New("coding agent command is required")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = cwd, stdin, stdout, stderr
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}

	wait := make(chan error, 1)
	go func() {
		wait <- cmd.Wait()
	}()
	for {
		select {
		case err := <-wait:
			return err
		case sig := <-signals:
			if err := cmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
				killErr := cmd.Process.Kill()
				waitErr := <-wait
				return errors.Join(fmt.Errorf("forward %s to coding agent process: %w", sig, err), killErr, waitErr)
			}
		}
	}
}

type startOptions struct {
	cwd          string
	socket       string
	timeout      time.Duration
	access       string
	notifyOnIdle bool
}

func parseStartOptions(args []string) (startOptions, error) {
	opts := startOptions{socket: cli.DefaultSocketPath, timeout: defaultTimeout, access: string(codingagent.AccessFull)}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--notify-idle":
			if hasValue {
				return startOptions{}, fmt.Errorf("--notify-idle does not accept a value")
			}
			opts.notifyOnIdle = true
		case "--cwd", "--socket", "--timeout", "--access":
			if !hasValue {
				if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
					return startOptions{}, fmt.Errorf("%s requires a value", name)
				}
				index++
				value = args[index]
			}
			switch name {
			case "--cwd":
				opts.cwd = value
			case "--socket":
				opts.socket = value
			case "--timeout":
				parsed, err := time.ParseDuration(value)
				if err != nil {
					return startOptions{}, fmt.Errorf("invalid --timeout %q: %w", value, err)
				}
				opts.timeout = parsed
			case "--access":
				access, err := codingagent.ParseAccessMode(value)
				if err != nil {
					return startOptions{}, err
				}
				opts.access = string(access)
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return startOptions{}, fmt.Errorf("unknown start option: %s", arg)
			}
			return startOptions{}, fmt.Errorf("unexpected start argument before --: %s", arg)
		}
	}
	return opts, nil
}

func splitPassthrough(args []string) (before, extra []string) {
	for index, arg := range args {
		if arg == "--" {
			return args[:index], args[index+1:]
		}
	}
	return args, nil
}

func resolveCWD(value string, getwd func() (string, error)) (string, error) {
	cwd := strings.TrimSpace(value)
	if cwd == "" {
		var err error
		cwd, err = getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(cwd)
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

func isNilAgentClient(client AgentClient) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func supportedKind(kind string) bool {
	switch kind {
	case "codex", "claude", "gemini", "cursor", "hermes":
		return true
	default:
		return false
	}
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func normalizeZellijPaneID(value string) string {
	id := strings.TrimSpace(value)
	if numeric, err := strconv.ParseUint(id, 10, 64); err == nil {
		return fmt.Sprintf("terminal_%d", numeric)
	}
	return id
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent agent <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  start   Manage a coding agent in the current Zellij pane")
	fmt.Fprintln(w, "  next    Focus the next managed coding agent")
	fmt.Fprintln(w, "  prev    Focus the previous managed coding agent")
	fmt.Fprintln(w, "  dashboard  Show and focus managed coding agents")
	fmt.Fprintln(w)
	printStartSummary(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'zellij-agent agent <command> --help' for command options.")
}

func printNextUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent agent next [--socket PATH --timeout DURATION --idle-only --pinned-only]")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  --socket PATH\n    agentd Unix socket path (default %q)\n", cli.DefaultSocketPath)
	fmt.Fprintln(w, "  --timeout DURATION")
	fmt.Fprintln(w, "    request timeout (default 10s; must be positive)")
	fmt.Fprintln(w, "  --idle-only")
	fmt.Fprintln(w, "    cycle only through agents whose detected state is idle")
	fmt.Fprintln(w, "  --pinned-only")
	fmt.Fprintln(w, "    cycle only through pinned agents")
}

func printPreviousUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent agent prev [--socket PATH --timeout DURATION --idle-only --pinned-only]")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  --socket PATH\n    agentd Unix socket path (default %q)\n", cli.DefaultSocketPath)
	fmt.Fprintln(w, "  --timeout DURATION")
	fmt.Fprintln(w, "    request timeout (default 10s; must be positive)")
	fmt.Fprintln(w, "  --idle-only")
	fmt.Fprintln(w, "    cycle only through agents whose detected state is idle")
	fmt.Fprintln(w, "  --pinned-only")
	fmt.Fprintln(w, "    cycle only through pinned agents")
}

func printDashboardUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent agent dashboard [--socket PATH --timeout DURATION --refresh-interval DURATION] [--output tui|json] [--focus AGENT_ID]")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  --socket PATH\n    agentd Unix socket path (default %q)\n", cli.DefaultSocketPath)
	fmt.Fprintln(w, "  --timeout DURATION")
	fmt.Fprintln(w, "    request timeout (default 10s)")
	fmt.Fprintln(w, "  --refresh-interval DURATION")
	fmt.Fprintln(w, "    polling refresh interval (default 2s)")
	fmt.Fprintln(w, "  --output tui|json")
	fmt.Fprintln(w, "    interactive dashboard or one-shot machine-readable output (default tui)")
	fmt.Fprintln(w, "  --focus AGENT_ID")
	fmt.Fprintln(w, "    focus one agent and exit; requires --output json")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Keys: j/k/Tab move, Space pin/unpin, Enter focus and exit, R refresh, q quit")
}

func printStartUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent agent start <codex|claude|gemini|cursor|hermes> [--cwd DIR --socket PATH --timeout DURATION --access full|read-only] [-- full-access arguments | read-only-prompt]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  --cwd DIR")
	fmt.Fprintln(w, "    default: current working directory; must be an existing directory")
	fmt.Fprintf(w, "  --socket PATH\n    agentd Unix socket path (default %q)\n", cli.DefaultSocketPath)
	fmt.Fprintln(w, "  --timeout DURATION")
	fmt.Fprintln(w, "    request timeout (default 10s; must be greater than 0)")
	fmt.Fprintln(w, "  --access full|read-only")
	fmt.Fprintln(w, "    default: full; read-only is supported only by Codex")
	fmt.Fprintln(w, "  --notify-idle")
	fmt.Fprintln(w, "    announce non-idle to idle state transitions")
	fmt.Fprintln(w, "  -- passthrough")
	fmt.Fprintln(w, "    full access passes remaining arguments unchanged; read-only accepts zero or one non-option prompt")
	fmt.Fprintln(w)
	printStartSummary(w)
}

func printStartSummary(w io.Writer) {
	fmt.Fprintln(w, "Start kinds: codex, claude, gemini, cursor, hermes")
	fmt.Fprintln(w, "CWD default: current working directory")
	fmt.Fprintln(w, "Start claims and manages the current Zellij pane.")
	fmt.Fprintln(w, "Start closes the managed pane when the agent exits.")
	fmt.Fprintln(w, "Use -- passthrough to pass remaining arguments unchanged.")
	fmt.Fprintln(w, "Access: --access full|read-only (default full; read-only is Codex only).")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  zellij-agent agent start codex --access full -- \"Implement M1\"")
	fmt.Fprintln(w, "  zellij-agent agent start codex --access read-only -- \"Verify M1\"")
	fmt.Fprintln(w, "Full-access profiles add their permission-bypass defaults before passthrough arguments:")
	fmt.Fprintln(w, "  codex   codex --dangerously-bypass-approvals-and-sandbox")
	fmt.Fprintln(w, "  claude  claude --dangerously-skip-permissions")
	fmt.Fprintln(w, "  gemini  agy --dangerously-skip-permissions")
	fmt.Fprintln(w, "  cursor  agent --yolo --trust")
}
