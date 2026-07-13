package dashboardcli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/dashboard"
)

type ClientFactory func(socketPath string, timeout time.Duration) dashboard.Client
type ModelFactory func(context.Context, dashboard.Client, dashboard.Options) tea.Model
type ProgramRunner func(context.Context, tea.Model, io.Reader, io.Writer) error

type Config struct {
	NewModel   ModelFactory
	RunProgram ProgramRunner
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		printUsage(stdout)
		return 0
	}

	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	refreshInterval := fs.Duration("refresh-interval", 2*time.Second, "polling refresh interval")
	eventLimit := fs.Int("event-limit", 100, "number of recent semantic events")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "dashboard does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "dashboard --timeout must be positive")
		return 2
	}
	if *refreshInterval <= 0 {
		fmt.Fprintln(stderr, "dashboard --refresh-interval must be positive")
		return 2
	}
	if *eventLimit <= 0 {
		fmt.Fprintln(stderr, "dashboard --event-limit must be positive")
		return 2
	}

	newModel := cfg.NewModel
	if newModel == nil {
		newModel = func(ctx context.Context, client dashboard.Client, opts dashboard.Options) tea.Model {
			return dashboard.NewModel(ctx, client, opts)
		}
	}
	runner := cfg.RunProgram
	if runner == nil {
		runner = runProgram
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := newClient(*socketPath, *timeout)
	model := newModel(ctx, client, dashboard.Options{RefreshInterval: *refreshInterval, EventLimit: *eventLimit})
	if err := runner(ctx, model, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dashboard failed: %v\n", err)
		return 1
	}
	return 0
}

func runProgram(ctx context.Context, model tea.Model, stdin io.Reader, stdout io.Writer) error {
	_, err := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
		tea.WithAltScreen(),
	).Run()
	return err
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent dashboard [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintf(w, "  --socket string\n    \tagentd Unix socket path (default %q)\n", cli.DefaultSocketPath)
	fmt.Fprintln(w, "  --timeout duration")
	fmt.Fprintln(w, "    \trequest timeout (default 10s)")
	fmt.Fprintln(w, "  --refresh-interval duration")
	fmt.Fprintln(w, "    \tpolling refresh interval (default 2s)")
	fmt.Fprintln(w, "  --event-limit int")
	fmt.Fprintln(w, "    \tnumber of recent semantic events (default 100)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Keys: j/k move, enter expand, s snapshot, i input, r reconcile, x cleanup, R refresh, q quit")
}
