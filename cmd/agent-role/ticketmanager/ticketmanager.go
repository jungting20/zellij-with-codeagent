package ticketmanager

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

type options struct {
	SocketPath     string
	TaskID         string
	AnchorPaneID   string
	ZellijSession  string
	RoleBin        string
	StartupTimeout time.Duration
	Path           string
}

type managerRunner interface {
	Run(context.Context) error
}

type dependencies struct {
	newClient        func(transport.ClientOptions) ticketworker.ManagerClient
	newVoiceNotifier func(io.Writer) ticketworker.VoiceNotifier
	newManager       func(ticketworker.ManagerOptions) (managerRunner, error)
}

func defaultDependencies() dependencies {
	return dependencies{
		newClient: func(opts transport.ClientOptions) ticketworker.ManagerClient {
			return transport.NewClient(opts)
		},
		newVoiceNotifier: ticketworker.NewNativeVoiceNotifier,
		newManager: func(opts ticketworker.ManagerOptions) (managerRunner, error) {
			return ticketworker.NewManager(opts)
		},
	}
}

func Run(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWithDependencies(ctx, args, os.Stdout, os.Stderr, defaultDependencies())
}

func runWithDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, deps dependencies) int {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	root, err := ticketworker.FindRoot(opts.Path)
	if err != nil {
		fmt.Fprintf(stderr, "Error: resolve ticket-manager repository: %v\n", err)
		return 1
	}
	cfg, err := ticketworker.LoadConfig(root)
	if err != nil {
		fmt.Fprintf(stderr, "Error: load ticket-worker config: %v\n", err)
		return 1
	}
	store, err := ticketworker.OpenExisting(ctx, root, nil)
	if err != nil {
		fmt.Fprintf(stderr, "Error: open ticket-worker store: %v\n", err)
		return 1
	}
	defer store.Close()

	client := deps.newClient(transport.ClientOptions{SocketPath: opts.SocketPath, Timeout: transport.DefaultRequestTimeout})
	var voiceNotifier ticketworker.VoiceNotifier
	if cfg.VoiceNotifications {
		voiceNotifier = deps.newVoiceNotifier(stdout)
	}
	manager, err := deps.newManager(ticketworker.ManagerOptions{
		Store: store, Client: client, Config: cfg, VoiceNotifier: voiceNotifier,
		Root: root, TaskID: opts.TaskID, AnchorPaneID: opts.AnchorPaneID,
		ZellijSession: opts.ZellijSession, RoleBin: opts.RoleBin,
		StartupTimeout: opts.StartupTimeout, Log: stdout,
	})
	if err != nil {
		if voiceNotifier != nil {
			if closeErr := voiceNotifier.Close(); closeErr != nil {
				fmt.Fprintf(stderr, "Error: close ticket-manager voice notifier: %v\n", closeErr)
			}
		}
		fmt.Fprintf(stderr, "Error: configure ticket-manager: %v\n", err)
		return 1
	}
	if err := manager.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "Error: run ticket-manager: %v\n", err)
		return 1
	}
	return 0
}

func parseOptions(args []string, output io.Writer) (options, error) {
	opts := options{
		SocketPath:     cli.DefaultSocketPath,
		RoleBin:        "zellij-agent",
		StartupTimeout: 15 * time.Second,
	}
	fs := flag.NewFlagSet("ticket-manager", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&opts.SocketPath, "socket", opts.SocketPath, "agentd Unix socket path")
	fs.StringVar(&opts.TaskID, "task", "", "logical runtime task ID")
	fs.StringVar(&opts.AnchorPaneID, "anchor-pane", "", "logical ticket-manager pane ID")
	fs.StringVar(&opts.ZellijSession, "zellij-session", "", "target Zellij session name")
	fs.StringVar(&opts.RoleBin, "role-bin", opts.RoleBin, "executable used to run child roles")
	fs.DurationVar(&opts.StartupTimeout, "startup-timeout", opts.StartupTimeout, "anchor and coding-agent readiness timeout")
	fs.Usage = func() {
		fmt.Fprintln(output, "Usage: agent-role ticket-manager [options] <path>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 1 {
		return options{}, fmt.Errorf("usage: agent-role ticket-manager [options] <path>")
	}
	opts.Path = fs.Arg(0)
	opts.TaskID = strings.TrimSpace(opts.TaskID)
	if opts.TaskID == "" {
		return options{}, fmt.Errorf("--task is required")
	}
	opts.AnchorPaneID = strings.TrimSpace(opts.AnchorPaneID)
	if opts.AnchorPaneID == "" {
		return options{}, fmt.Errorf("--anchor-pane is required")
	}
	opts.RoleBin = strings.TrimSpace(opts.RoleBin)
	if opts.RoleBin == "" {
		return options{}, fmt.Errorf("--role-bin must not be empty")
	}
	if opts.StartupTimeout <= 0 {
		return options{}, fmt.Errorf("--startup-timeout must be positive")
	}
	zellijSession, err := cli.ResolveZellijSession(opts.ZellijSession)
	if err != nil {
		return options{}, err
	}
	opts.ZellijSession = zellijSession
	return opts, nil
}
