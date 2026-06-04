package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"zellij-with-codeagent/internal/transport"
)

const defaultSocketPath = "/tmp/agentd.sock"

type agentClient interface {
	Health(context.Context) (transport.HealthResponse, error)
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
	RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error)
	Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error)
}

type clientFactory func(socketPath string, timeout time.Duration) agentClient

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, func(socketPath string, timeout time.Duration) agentClient {
		return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
	}))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient clientFactory) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "health":
		return runHealth(args[1:], stdout, stderr, newClient)
	case "status":
		return runStatus(args[1:], stdout, stderr, newClient)
	case "plan":
		return runPlan(args[1:], stdin, stdout, stderr, newClient)
	case "events":
		return runEvents(args[1:], stdout, stderr, newClient)
	case "cleanup":
		return runCleanup(args[1:], stdout, stderr, newClient)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runHealth(args []string, stdout, stderr io.Writer, newClient clientFactory) int {
	fs, opts := newFlagSet("health", stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	response, err := newClient(opts.socketPath, opts.timeout).Health(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl health failed: %v\n", err)
		return 1
	}
	if response.Version != "" {
		fmt.Fprintf(stdout, "agentd %s (%s)\n", response.Status, response.Version)
		return 0
	}
	fmt.Fprintf(stdout, "agentd %s\n", response.Status)
	return 0
}

func runStatus(args []string, stdout, stderr io.Writer, newClient clientFactory) int {
	fs, opts := newFlagSet("status", stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	response, err := newClient(opts.socketPath, opts.timeout).InspectRuntime(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl status failed: %v\n", err)
		return 1
	}
	printRuntimeStatus(stdout, response)
	return 0
}

func runPlan(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient clientFactory) int {
	fs, opts := newFlagSet("plan", stderr)
	filePath := fs.String("file", "", "JSON execution plan file, or - for stdin")
	requestID := fs.String("request-id", "", "request id override")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *filePath == "" {
		fmt.Fprintln(stderr, "plan requires --file <path>")
		return 2
	}

	payload, resolvedRequestID, err := loadExecutionPlan(*filePath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read execution plan: %v\n", err)
		return 1
	}
	if *requestID != "" {
		resolvedRequestID = *requestID
	}
	if resolvedRequestID == "" {
		resolvedRequestID = fmt.Sprintf("req_%d", time.Now().Unix())
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	response, err := newClient(opts.socketPath, opts.timeout).SubmitExecutionPlan(ctx, resolvedRequestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl plan failed: %v\n", err)
		return 1
	}
	printExecutionPlanResponse(stdout, response)
	return 0
}

func runEvents(args []string, stdout, stderr io.Writer, newClient clientFactory) int {
	fs, opts := newFlagSet("events", stderr)
	limit := fs.Int("limit", 20, "maximum number of recent events")
	var eventTypes repeatedStrings
	fs.Var(&eventTypes, "type", "event type filter; can be repeated")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	response, err := newClient(opts.socketPath, opts.timeout).RecentEvents(ctx, *limit, eventTypes...)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl events failed: %v\n", err)
		return 1
	}
	printEvents(stdout, response.Events)
	return 0
}

func runCleanup(args []string, stdout, stderr io.Writer, newClient clientFactory) int {
	fs, opts := newFlagSet("cleanup", stderr)
	var paneIDs repeatedStrings
	fs.Var(&paneIDs, "pane", "logical pane id to close; can be repeated")
	taskID := fs.String("task", "", "task id filter")
	role := fs.String("role", "", "role filter")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	response, err := newClient(opts.socketPath, opts.timeout).Cleanup(ctx, transport.CleanupRequest{
		PaneIDs: paneIDs,
		TaskID:  *taskID,
		Role:    *role,
	})
	if err != nil {
		fmt.Fprintf(stderr, "agentctl cleanup failed: %v\n", err)
		return 1
	}
	printCleanupResponse(stdout, response)
	return 0
}

type commandOptions struct {
	socketPath string
	timeout    time.Duration
}

func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *commandOptions) {
	opts := &commandOptions{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.socketPath, "socket", defaultSocketPath, "agentd Unix socket path")
	fs.DurationVar(&opts.timeout, "timeout", 10*time.Second, "request timeout")
	return fs, opts
}

func loadExecutionPlan(filePath string, stdin io.Reader) (transport.ExecutionPlanPayload, string, error) {
	var data []byte
	var err error
	if filePath == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(filePath)
	}
	if err != nil {
		return transport.ExecutionPlanPayload{}, "", err
	}

	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(data, &envelope); err == nil && (envelope.Type != "" || len(envelope.Payload) > 0) {
		if envelope.Type != transport.RequestTypeExecutionPlan {
			return transport.ExecutionPlanPayload{}, "", fmt.Errorf("unsupported request type %q", envelope.Type)
		}
		if len(envelope.Payload) == 0 {
			return transport.ExecutionPlanPayload{}, "", errors.New("execution_plan payload is required")
		}
		var payload transport.ExecutionPlanPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return transport.ExecutionPlanPayload{}, "", err
		}
		return payload, envelope.RequestID, nil
	}

	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return transport.ExecutionPlanPayload{}, "", err
	}
	return payload, "", nil
}

func printRuntimeStatus(w io.Writer, response transport.InspectRuntimeResponse) {
	if response.Message != "" {
		fmt.Fprintln(w, response.Message)
	}
	fmt.Fprintf(w, "managed=%d active=%d terminal=%d running=%d starting=%d error=%d\n",
		response.Counts.Managed,
		response.Counts.Active,
		response.Counts.Terminal,
		response.Counts.Running,
		response.Counts.Starting,
		response.Counts.Error,
	)
	if len(response.Panes) == 0 {
		fmt.Fprintln(w, "panes: none")
		return
	}
	fmt.Fprintln(w, "panes:")
	for _, pane := range response.Panes {
		fmt.Fprintf(w, "- %s role=%s task=%s status=%s zellij=%s\n", pane.ID, pane.Role, pane.TaskID, pane.Status, pane.ZellijPaneID)
	}
}

func printExecutionPlanResponse(w io.Writer, response transport.ExecutionPlanResponse) {
	fmt.Fprintf(w, "request=%s session=%s layout=%s\n", response.RequestID, response.Session, response.Layout)
	for _, tab := range response.Tabs {
		fmt.Fprintf(w, "tab=%s panes=%d\n", tab.Name, len(tab.Panes))
		for _, pane := range tab.Panes {
			fmt.Fprintf(w, "- %s role=%s status=%s zellij=%s\n", pane.ID, pane.Role, pane.Status, pane.ZellijPaneID)
		}
	}
}

func printEvents(w io.Writer, events []transport.Event) {
	if len(events) == 0 {
		fmt.Fprintln(w, "events: none")
		return
	}
	for _, event := range events {
		fmt.Fprintf(w, "%s type=%s pane=%s task=%s message=%s\n",
			event.Time.Format(time.RFC3339),
			event.Type,
			event.PaneID,
			event.TaskID,
			event.Message,
		)
	}
}

func printCleanupResponse(w io.Writer, response transport.CleanupResponse) {
	fmt.Fprintf(w, "closed=%d failed=%d skipped=%d\n", len(response.Closed), len(response.Failed), len(response.Skipped))
	for _, pane := range response.Closed {
		fmt.Fprintf(w, "- closed %s status=%s\n", pane.ID, pane.Status)
	}
	for _, failure := range response.Failed {
		fmt.Fprintf(w, "- failed %s: %s\n", failure.Pane.ID, failure.Error)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agentctl <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  health    Check the agentd socket")
	fmt.Fprintln(w, "  status    Inspect managed runtime state")
	fmt.Fprintln(w, "  plan      Submit an execution plan JSON file")
	fmt.Fprintln(w, "  events    Print recent runtime events")
	fmt.Fprintln(w, "  cleanup   Close managed panes")
}

type repeatedStrings []string

func (values *repeatedStrings) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedStrings) Set(value string) error {
	if value != "" {
		*values = append(*values, value)
	}
	return nil
}
