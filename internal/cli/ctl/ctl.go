package ctlcli

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

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/debate"
	"zellij-with-codeagent/internal/planner"
	"zellij-with-codeagent/internal/transport"
)

type AgentClient interface {
	Health(context.Context) (transport.HealthResponse, error)
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
	SendInput(context.Context, string, transport.SendInputRequest) error
	SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error)
	SendMessage(context.Context, transport.SendMessageRequest) (transport.SendMessageResponse, error)
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
	RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
	Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error)
}

type ClientFactory func(socketPath string, timeout time.Duration) AgentClient

func main() {
	os.Exit(Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, func(socketPath string, timeout time.Duration) AgentClient {
		return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
	}))
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory) int {
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
	case "debate":
		return runDebate(args[1:], stdout, stderr, newClient)
	case "input":
		return runInput(args[1:], stdin, stdout, stderr, newClient)
	case "snapshot":
		return runSnapshot(args[1:], stdout, stderr, newClient)
	case "message":
		return runMessage(args[1:], stdin, stdout, stderr, newClient)
	case "forward-snapshot":
		return runForwardSnapshot(args[1:], stdout, stderr, newClient)
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

func runHealth(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("health", stderr)
	if err := parseInterspersed(fs, args); err != nil {
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

func runStatus(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("status", stderr)
	if err := parseInterspersed(fs, args); err != nil {
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

func runInput(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("input", stderr)
	text := fs.String("text", "", "text to send to the pane")
	filePath := fs.String("file", "", "file containing text to send, or - for stdin")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "input requires <pane-id>")
		return 2
	}
	payload, err := readTextPayload(*text, *filePath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read input text: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	paneID := fs.Arg(0)
	if err := newClient(opts.socketPath, opts.timeout).SendInput(ctx, paneID, transport.SendInputRequest{Text: payload}); err != nil {
		fmt.Fprintf(stderr, "agentctl input failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sent input pane=%s bytes=%d\n", paneID, len(payload))
	return 0
}

func runSnapshot(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("snapshot", stderr)
	full := fs.Bool("full", false, "dump full scrollback when supported")
	ansi := fs.Bool("ansi", false, "preserve ANSI escape sequences")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "snapshot requires <pane-id>")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	response, err := newClient(opts.socketPath, opts.timeout).SnapshotOutput(ctx, fs.Arg(0), transport.SnapshotOutputRequest{
		Full: *full,
		ANSI: *ansi,
	})
	if err != nil {
		fmt.Fprintf(stderr, "agentctl snapshot failed: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, response.Output)
	return 0
}

func runMessage(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("message", stderr)
	from := fs.String("from", "", "source logical pane id")
	to := fs.String("to", "", "target logical pane id")
	messageType := fs.String("type", "message", "message type")
	body := fs.String("body", "", "message body")
	filePath := fs.String("file", "", "file containing message body, or - for stdin")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if *from == "" || *to == "" {
		fmt.Fprintln(stderr, "message requires --from <pane-id> and --to <pane-id>")
		return 2
	}
	payload, err := readTextPayload(*body, *filePath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read message body: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	response, err := newClient(opts.socketPath, opts.timeout).SendMessage(ctx, transport.SendMessageRequest{
		From: *from,
		To:   *to,
		Type: *messageType,
		Body: payload,
	})
	if err != nil {
		fmt.Fprintf(stderr, "agentctl message failed: %v\n", err)
		return 1
	}
	printMessageResponse(stdout, response)
	return 0
}

func runForwardSnapshot(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("forward-snapshot", stderr)
	full := fs.Bool("full", false, "dump full scrollback when supported")
	ansi := fs.Bool("ansi", false, "preserve ANSI escape sequences")
	messageType := fs.String("type", "screen_dump", "message type")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "forward-snapshot requires <source-pane-id> <target-pane-id>")
		return 2
	}
	sourcePaneID := fs.Arg(0)
	targetPaneID := fs.Arg(1)

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	client := newClient(opts.socketPath, opts.timeout)
	snapshot, err := client.SnapshotOutput(ctx, sourcePaneID, transport.SnapshotOutputRequest{
		Full: *full,
		ANSI: *ansi,
	})
	if err != nil {
		fmt.Fprintf(stderr, "agentctl forward-snapshot snapshot failed: %v\n", err)
		return 1
	}
	response, err := client.SendMessage(ctx, transport.SendMessageRequest{
		From: sourcePaneID,
		To:   targetPaneID,
		Type: *messageType,
		Body: snapshot.Output,
	})
	if err != nil {
		fmt.Fprintf(stderr, "agentctl forward-snapshot message failed: %v\n", err)
		return 1
	}
	printMessageResponse(stdout, response)
	return 0
}

func runPlan(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("plan", stderr)
	filePath := fs.String("file", "", "JSON execution plan file, or - for stdin")
	requestID := fs.String("request-id", "", "request id override")
	zellijSessionFlag := fs.String("zellij-session", "", "physical Zellij session; defaults to ZELLIJ_SESSION_NAME")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *filePath == "" {
		fmt.Fprintln(stderr, "plan requires --file <path>")
		return 2
	}

	plan, err := loadExecutionPlan(*filePath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "plan decode failed: %v\n", err)
		return 1
	}
	sessionCandidate := plan.Payload.ZellijSession
	if strings.TrimSpace(*zellijSessionFlag) != "" {
		sessionCandidate = *zellijSessionFlag
	}
	zellijSession, err := cli.ResolveZellijSession(sessionCandidate)
	if err != nil {
		fmt.Fprintf(stderr, "resolve zellij session: %v\n", err)
		return 1
	}
	plan.Payload.ZellijSession = zellijSession
	if err := planner.ValidateExecutionPlan(plan); err != nil {
		fmt.Fprintf(stderr, "plan validation failed: %v\n", err)
		return 1
	}
	if err := rebuildExecutionPlanEnvelope(&plan); err != nil {
		fmt.Fprintf(stderr, "plan encode failed: %v\n", err)
		return 1
	}
	payload := plan.Payload
	resolvedRequestID := plan.Envelope.RequestID
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

func runDebate(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("debate", stderr)
	opts.timeout = 10 * time.Minute
	topic := fs.String("topic", "", "debate topic")
	agentsCSV := fs.String("agents", "agy,agent,codex", "comma-separated agent ids")
	rounds := fs.Int("rounds", 1, "number of debate rounds to run, from 1 to 3")
	agentTimeout := fs.Duration("agent-timeout", 2*time.Minute, "per-agent debate response timeout")
	configPath := fs.String("config", "", "YAML file defining debate agent commands")
	cwd := fs.String("cwd", ".", "working directory for coding-agent panes")
	agentRoleBin := fs.String("agent-role-bin", "", "zellij-agent binary used by generated panes")
	zellijSessionFlag := fs.String("zellij-session", "", "physical Zellij session; defaults to ZELLIJ_SESSION_NAME")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	zellijSession, err := cli.ResolveZellijSession(*zellijSessionFlag)
	if err != nil {
		fmt.Fprintf(stderr, "resolve zellij session: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	result, err := debate.Run(ctx, newClient(opts.socketPath, opts.timeout), debate.Options{
		Topic:         *topic,
		Agents:        debate.ParseAgents(*agentsCSV),
		Rounds:        *rounds,
		AgentTimeout:  *agentTimeout,
		ConfigPath:    *configPath,
		CWD:           *cwd,
		AgentRoleBin:  *agentRoleBin,
		ZellijSession: zellijSession,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		if debate.IsValidationError(err) {
			return 2
		}
		return 1
	}

	debate.PrintResult(stdout, result)
	return 0
}

func SetDebateDelayForTesting(fn func(context.Context, time.Duration) error) func() {
	return debate.SetDelayForTesting(fn)
}

func runEvents(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("events", stderr)
	limit := fs.Int("limit", 20, "maximum number of recent events")
	follow := fs.Bool("follow", false, "stream events until interrupted")
	var eventTypes repeatedStrings
	fs.Var(&eventTypes, "type", "event type filter; can be repeated")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *follow {
		return runFollowEvents(opts, eventTypes, stdout, stderr, newClient)
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

func runFollowEvents(opts *commandOptions, eventTypes []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := newClient(opts.socketPath, opts.timeout).StreamEvents(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl events --follow failed: %v\n", err)
		return 1
	}
	defer stream.Close()

	for {
		select {
		case event, ok := <-stream.Events:
			if !ok {
				return 0
			}
			if eventMatchesTypes(event, eventTypes) {
				printEvents(stdout, []transport.Event{event})
			}
		case err, ok := <-stream.Errors:
			if ok && err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(stderr, "agentctl events --follow failed: %v\n", err)
				return 1
			}
			return 0
		}
	}
}

func runCleanup(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
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
	fs.StringVar(&opts.socketPath, "socket", cli.DefaultSocketPath, "agentd Unix socket path")
	fs.DurationVar(&opts.timeout, "timeout", 10*time.Second, "request timeout")
	return fs, opts
}

func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var flagArgs []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
		name := strings.TrimLeft(arg, "-")
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			name = name[:idx]
		}
		if f := fs.Lookup(name); f != nil {
			if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue
			}
		}
		if strings.Contains(arg, "=") {
			continue
		}
		if i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return fs.Parse(append(flagArgs, positional...))
}

func loadExecutionPlan(filePath string, stdin io.Reader) (planner.ValidatedExecutionPlan, error) {
	var data []byte
	var err error
	if filePath == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(filePath)
	}
	if err != nil {
		return planner.ValidatedExecutionPlan{}, err
	}
	return planner.DecodeExecutionPlanEnvelope(data)
}

func rebuildExecutionPlanEnvelope(plan *planner.ValidatedExecutionPlan) error {
	payload, err := json.Marshal(plan.Payload)
	if err != nil {
		return err
	}
	plan.Envelope.Payload = payload
	return nil
}

func readTextPayload(text, filePath string, stdin io.Reader) (string, error) {
	if filePath != "" {
		data, err := readFileOrStdin(filePath, stdin)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return text, nil
}

func readFileOrStdin(filePath string, stdin io.Reader) ([]byte, error) {
	if filePath == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(filePath)
}

func eventMatchesTypes(event transport.Event, eventTypes []string) bool {
	if len(eventTypes) == 0 {
		return true
	}
	for _, eventType := range eventTypes {
		if event.Type == eventType {
			return true
		}
	}
	return false
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

func printMessageResponse(w io.Writer, response transport.SendMessageResponse) {
	fmt.Fprintf(w, "delivered from=%s to=%s type=%s bytes=%d\n",
		response.From.ID,
		response.To.ID,
		response.Type,
		len(response.Body),
	)
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
	fmt.Fprintln(w, "  debate    Run a multi-agent debate and synthesize the results")
	fmt.Fprintln(w, "  input     Send text to a managed pane")
	fmt.Fprintln(w, "  snapshot  Dump managed pane output")
	fmt.Fprintln(w, "  message   Send a tab-scoped message between managed panes")
	fmt.Fprintln(w, "  forward-snapshot")
	fmt.Fprintln(w, "            Send one pane's screen dump to another pane")
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
