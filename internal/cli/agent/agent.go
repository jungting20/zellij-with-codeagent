package agentcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/transport"
)

const defaultTimeout = 10 * time.Second

type AgentClient interface {
	StartAgent(context.Context, transport.StartAgentRequest) (transport.StartAgentResponse, error)
}

type ClientFactory func(socketPath string, timeout time.Duration) AgentClient

type Config struct {
	Getwd  func() (string, error)
	Getenv func(string) string
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	_ = stdin

	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "start":
		return runStart(args[1:], stdout, stderr, newClient, cfg)
	default:
		fmt.Fprintf(stderr, "unknown agent command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runStart(args []string, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	if len(args) == 1 && isHelp(args[0]) {
		printStartUsage(stdout)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "start requires <codex|claude|gemini|cursor>")
		return 2
	}

	kind := args[0]
	if !supportedKind(kind) {
		fmt.Fprintf(stderr, "unsupported agent kind: %s (want codex, claude, gemini, or cursor)\n", kind)
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

	cwd, err := resolveCWD(opts.cwd, cfg.Getwd)
	if err != nil {
		fmt.Fprintf(stderr, "resolve cwd: %v\n", err)
		return 2
	}

	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	session := strings.TrimSpace(getenv("ZELLIJ_SESSION_NAME"))
	if session == "" {
		fmt.Fprintln(stderr, "ZELLIJ_SESSION_NAME is required; agent start must run inside Zellij")
		return 2
	}
	paneID := strings.TrimSpace(getenv("ZELLIJ_PANE_ID"))
	if paneID == "" {
		fmt.Fprintln(stderr, "ZELLIJ_PANE_ID is required; agent start must run inside a Zellij pane")
		return 2
	}
	if newClient == nil {
		fmt.Fprintln(stderr, "agent start client is not configured")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	response, err := newClient(opts.socket, opts.timeout).StartAgent(ctx, transport.StartAgentRequest{
		Kind:               kind,
		CWD:                cwd,
		Args:               append([]string(nil), extra...),
		SourceSession:      session,
		SourceZellijPaneID: paneID,
	})
	if err != nil {
		fmt.Fprintf(stderr, "agent start failed via socket %s: %v\n", opts.socket, err)
		return 1
	}

	startedPaneID := response.Agent.Agent.PaneID
	if startedPaneID == "" {
		startedPaneID = response.Agent.Pane.ID
	}
	fmt.Fprintf(stdout, "started agent=%s kind=%s pane=%s\n", response.Agent.Agent.ID, kind, startedPaneID)
	return 0
}

type startOptions struct {
	cwd     string
	socket  string
	timeout time.Duration
}

func parseStartOptions(args []string) (startOptions, error) {
	opts := startOptions{socket: cli.DefaultSocketPath, timeout: defaultTimeout}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--cwd", "--socket", "--timeout":
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
		if getwd == nil {
			getwd = os.Getwd
		}
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

func supportedKind(kind string) bool {
	switch kind {
	case "codex", "claude", "gemini", "cursor":
		return true
	default:
		return false
	}
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent agent <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  start   Start a coding agent in the current Zellij tab")
	fmt.Fprintln(w)
	printStartSummary(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'zellij-agent agent start --help' for start options.")
}

func printStartUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent agent start <codex|claude|gemini|cursor> [--cwd DIR --socket PATH --timeout DURATION] [-- extra arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  --cwd DIR")
	fmt.Fprintln(w, "    default: current working directory; must be an existing directory")
	fmt.Fprintf(w, "  --socket PATH\n    agentd Unix socket path (default %q)\n", cli.DefaultSocketPath)
	fmt.Fprintln(w, "  --timeout DURATION")
	fmt.Fprintln(w, "    request timeout (default 10s; must be greater than 0)")
	fmt.Fprintln(w, "  -- passthrough")
	fmt.Fprintln(w, "    pass remaining arguments unchanged to the selected agent profile")
	fmt.Fprintln(w)
	printStartSummary(w)
}

func printStartSummary(w io.Writer) {
	fmt.Fprintln(w, "Start kinds: codex, claude, gemini, cursor")
	fmt.Fprintln(w, "CWD default: current working directory")
	fmt.Fprintln(w, "Use -- passthrough to pass remaining arguments unchanged.")
	fmt.Fprintln(w, "Profiles add their permission-bypass defaults before passthrough arguments:")
	fmt.Fprintln(w, "  codex   codex --dangerously-bypass-approvals-and-sandbox")
	fmt.Fprintln(w, "  claude  claude --dangerously-skip-permissions")
	fmt.Fprintln(w, "  gemini  agy --dangerously-skip-permissions")
	fmt.Fprintln(w, "  cursor  agent --yolo --trust")
}
