package ctlcli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"zellij-with-codeagent/internal/cli"
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

func runDebate(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs, opts := newFlagSet("debate", stderr)
	opts.timeout = 10 * time.Minute
	topic := fs.String("topic", "", "debate topic")
	agentsCSV := fs.String("agents", "a,b,c", "comma-separated agent ids")
	rounds := fs.Int("rounds", 1, "number of debate rounds to run, from 1 to 3")
	agentTimeout := fs.Duration("agent-timeout", 2*time.Minute, "per-agent debate response timeout")
	configPath := fs.String("config", "", "YAML file defining debate agent commands")
	cwd := fs.String("cwd", ".", "working directory for coding-agent panes")
	agentRoleBin := fs.String("agent-role-bin", "", "zellij-agent binary used by generated panes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*topic) == "" {
		fmt.Fprintln(stderr, "debate requires --topic <text>")
		return 2
	}
	agents := parseDebateAgents(*agentsCSV)
	if len(agents) == 0 {
		fmt.Fprintln(stderr, "debate requires at least one agent")
		return 2
	}
	if *rounds < 1 || *rounds > 3 {
		fmt.Fprintln(stderr, "debate requires --rounds between 1 and 3")
		return 2
	}
	if *agentTimeout <= 0 {
		fmt.Fprintln(stderr, "debate requires --agent-timeout greater than 0")
		return 2
	}
	agentSpecs := defaultDebateAgentSpecs(agents, *cwd, debateRoleCommand(*agentRoleBin))
	coordinatorSpec := defaultDebateCoordinatorSpec(*cwd, debateRoleCommand(*agentRoleBin))
	if strings.TrimSpace(*configPath) != "" {
		loadedAgents, loadedCoordinator, err := loadDebateConfig(*configPath, *cwd, coordinatorSpec)
		if err != nil {
			fmt.Fprintf(stderr, "debate config failed: %v\n", err)
			return 2
		}
		agentSpecs = loadedAgents
		coordinatorSpec = loadedCoordinator
		agents = debateAgentIDs(agentSpecs)
	}

	requestID := fmt.Sprintf("debate_%d", time.Now().UnixNano())
	payload := debateExecutionPlan(requestID, agentSpecs, coordinatorSpec)

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	client := newClient(opts.socketPath, opts.timeout)
	response, err := client.SubmitExecutionPlan(ctx, requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl debate plan failed: %v\n", err)
		return 1
	}
	roundOutputs := make([]debateRoundOutput, 0, *rounds)
	for round := 1; round <= *rounds; round++ {
		markers := debateMarkers(requestID, agents, round)
		for _, agent := range agents {
			paneID := "debate-" + agent
			agentSpec := debateAgentSpecByID(agentSpecs, agent)
			if err := sendDebateAgentInput(ctx, client, paneID, debateRoundPrompt(*topic, round, agent, roundOutputs, markers[paneID]), agentSpec); err != nil {
				fmt.Fprintf(stderr, "agentctl debate prompt failed: %v\n", err)
				return 1
			}
		}
		statuses, err := waitForDebateMarkers(ctx, client, markers, *agentTimeout)
		if err != nil {
			fmt.Fprintf(stderr, "agentctl debate wait failed: %v\n", err)
			return 1
		}
		agentOutputs := make(map[string]string, len(agents))
		for _, agent := range agents {
			paneID := "debate-" + agent
			snapshot, err := client.SnapshotOutput(ctx, paneID, transport.SnapshotOutputRequest{Full: true})
			if err != nil {
				fmt.Fprintf(stderr, "agentctl debate snapshot failed: %v\n", err)
				return 1
			}
			agentOutputs[paneID] = snapshot.Output
		}
		roundOutputs = append(roundOutputs, debateRoundOutput{Round: round, Outputs: agentOutputs, Statuses: statuses})
	}

	coordinatorMarker := debateCompletionMarker(requestID, *rounds, "coordinator")
	if err := client.SendInput(ctx, "debate-coordinator", transport.SendInputRequest{Text: debateSynthesisBlock(*topic, agents, roundOutputs, coordinatorMarker)}); err != nil {
		fmt.Fprintf(stderr, "agentctl debate synthesis prompt failed: %v\n", err)
		return 1
	}
	coordinatorStatuses, err := waitForDebateMarkers(ctx, client, map[string]string{"debate-coordinator": coordinatorMarker}, *agentTimeout)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl debate synthesis wait failed: %v\n", err)
		return 1
	}
	if status := coordinatorStatuses["debate-coordinator"]; status.Status != debatePaneStatusDone {
		fmt.Fprintf(stderr, "agentctl debate synthesis wait failed: debate-coordinator %s after %s\n", status.Status, status.Timeout)
		return 1
	}
	coordinatorSnapshot, err := client.SnapshotOutput(ctx, "debate-coordinator", transport.SnapshotOutputRequest{Full: true})
	if err != nil {
		fmt.Fprintf(stderr, "agentctl debate coordinator snapshot failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "debate request=%s session=%s agents=%s\n", response.RequestID, response.Session, strings.Join(agents, ","))
	printDebateStatus(stdout, roundOutputs, agents)
	for _, roundOutput := range roundOutputs {
		for _, agent := range agents {
			paneID := "debate-" + agent
			fmt.Fprintf(stdout, "\n[round %d %s]\n%s\n", roundOutput.Round, paneID, roundOutput.Outputs[paneID])
		}
	}
	fmt.Fprintf(stdout, "\n[debate-coordinator synthesis]\n%s\n", coordinatorSnapshot.Output)
	return 0
}

type debateRoundOutput struct {
	Round    int
	Outputs  map[string]string
	Statuses map[string]debatePaneWaitStatus
}

const (
	debatePaneStatusDone     = "done"
	debatePaneStatusTimedOut = "timed_out"
)

type debatePaneWaitStatus struct {
	PaneID  string
	Status  string
	Elapsed time.Duration
	Timeout time.Duration
}

type debateAgentSpec struct {
	ID                 string
	Role               string
	Command            []string
	CWD                string
	SubmitNewlines     int
	ExtraSubmitEnters  int
	ExtraSubmitDelayMS int
}

type debateCoordinatorSpec struct {
	Role    string
	Command []string
	CWD     string
}

type debateConfigFile struct {
	Agents      []debateAgentConfig `yaml:"agents"`
	Coordinator debatePaneConfig    `yaml:"coordinator"`
}

type debateAgentConfig struct {
	ID                 string   `yaml:"id"`
	Role               string   `yaml:"role"`
	Command            []string `yaml:"command"`
	CWD                string   `yaml:"cwd"`
	SubmitNewlines     int      `yaml:"submit_newlines"`
	ExtraSubmitEnters  int      `yaml:"extra_submit_enters"`
	ExtraSubmitDelayMS int      `yaml:"extra_submit_delay_ms"`
}

type debatePaneConfig struct {
	Role    string   `yaml:"role"`
	Command []string `yaml:"command"`
	CWD     string   `yaml:"cwd"`
}

func parseDebateAgents(raw string) []string {
	parts := strings.Split(raw, ",")
	agents := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		agent := strings.TrimSpace(part)
		if agent == "" {
			continue
		}
		if _, exists := seen[agent]; exists {
			continue
		}
		seen[agent] = struct{}{}
		agents = append(agents, agent)
	}
	return agents
}

func defaultDebateAgentSpecs(agents []string, cwd string, roleCommand []string) []debateAgentSpec {
	specs := make([]debateAgentSpec, 0, len(agents))
	for _, agent := range agents {
		specs = append(specs, debateAgentSpec{
			ID:             agent,
			Role:           "coding-agent",
			Command:        append(cloneStringSlice(roleCommand), "coding-agent", cwd),
			CWD:            cwd,
			SubmitNewlines: 1,
		})
	}
	return specs
}

func defaultDebateCoordinatorSpec(cwd string, roleCommand []string) debateCoordinatorSpec {
	return debateCoordinatorSpec{
		Role:    "debate-coordinator",
		Command: append(cloneStringSlice(roleCommand), "debate-coordinator", cwd),
		CWD:     cwd,
	}
}

func loadDebateConfig(path string, defaultCWD string, defaultCoordinator debateCoordinatorSpec) ([]debateAgentSpec, debateCoordinatorSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, debateCoordinatorSpec{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg debateConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, debateCoordinatorSpec{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Agents) == 0 {
		return nil, debateCoordinatorSpec{}, fmt.Errorf("agents are required")
	}
	seen := make(map[string]struct{}, len(cfg.Agents))
	agents := make([]debateAgentSpec, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		id := strings.TrimSpace(agent.ID)
		if id == "" {
			return nil, debateCoordinatorSpec{}, fmt.Errorf("agent id is required")
		}
		if _, ok := seen[id]; ok {
			return nil, debateCoordinatorSpec{}, fmt.Errorf("duplicate agent id %q", id)
		}
		seen[id] = struct{}{}
		if len(agent.Command) == 0 {
			return nil, debateCoordinatorSpec{}, fmt.Errorf("agent %s command is required", id)
		}
		role := strings.TrimSpace(agent.Role)
		if role == "" {
			role = "coding-agent"
		}
		cwd := strings.TrimSpace(agent.CWD)
		if cwd == "" {
			cwd = defaultCWD
		}
		submitNewlines := agent.SubmitNewlines
		if submitNewlines == 0 {
			submitNewlines = 1
		}
		if submitNewlines < 1 {
			return nil, debateCoordinatorSpec{}, fmt.Errorf("agent %s submit_newlines must be at least 1", id)
		}
		if agent.ExtraSubmitEnters < 0 {
			return nil, debateCoordinatorSpec{}, fmt.Errorf("agent %s extra_submit_enters must not be negative", id)
		}
		if agent.ExtraSubmitDelayMS < 0 {
			return nil, debateCoordinatorSpec{}, fmt.Errorf("agent %s extra_submit_delay_ms must not be negative", id)
		}
		agents = append(agents, debateAgentSpec{
			ID:                 id,
			Role:               role,
			Command:            cloneStringSlice(agent.Command),
			CWD:                cwd,
			SubmitNewlines:     submitNewlines,
			ExtraSubmitEnters:  agent.ExtraSubmitEnters,
			ExtraSubmitDelayMS: agent.ExtraSubmitDelayMS,
		})
	}

	coordinator := defaultCoordinator
	if len(cfg.Coordinator.Command) > 0 {
		coordinator.Command = cloneStringSlice(cfg.Coordinator.Command)
	}
	if strings.TrimSpace(cfg.Coordinator.Role) != "" {
		coordinator.Role = strings.TrimSpace(cfg.Coordinator.Role)
	}
	if strings.TrimSpace(cfg.Coordinator.CWD) != "" {
		coordinator.CWD = strings.TrimSpace(cfg.Coordinator.CWD)
	}
	return agents, coordinator, nil
}

func debateAgentIDs(agents []debateAgentSpec) []string {
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		ids = append(ids, agent.ID)
	}
	return ids
}

func debateAgentSpecByID(agents []debateAgentSpec, id string) debateAgentSpec {
	for _, agent := range agents {
		if agent.ID == id {
			return agent
		}
	}
	return debateAgentSpec{ID: id, SubmitNewlines: 1}
}

func sendDebateAgentInput(ctx context.Context, client AgentClient, paneID string, prompt string, agent debateAgentSpec) error {
	if err := client.SendInput(ctx, paneID, transport.SendInputRequest{Text: withTrailingNewlines(prompt, agent.SubmitNewlines)}); err != nil {
		return err
	}
	for i := 0; i < agent.ExtraSubmitEnters; i++ {
		if agent.ExtraSubmitDelayMS > 0 {
			timer := time.NewTimer(time.Duration(agent.ExtraSubmitDelayMS) * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if err := client.SendInput(ctx, paneID, transport.SendInputRequest{Text: "\n"}); err != nil {
			return err
		}
	}
	return nil
}

func withTrailingNewlines(text string, count int) string {
	if count < 1 {
		count = 1
	}
	return strings.TrimRight(text, "\n") + strings.Repeat("\n", count)
}

func debateExecutionPlan(requestID string, agents []debateAgentSpec, coordinator debateCoordinatorSpec) transport.ExecutionPlanPayload {
	panes := []transport.ExecutionPlanPane{
		{
			ID:      "debate-coordinator",
			Role:    coordinator.Role,
			AgentID: "coordinator",
			Command: cloneStringSlice(coordinator.Command),
			CWD:     coordinator.CWD,
		},
	}
	for _, agent := range agents {
		panes = append(panes, transport.ExecutionPlanPane{
			ID:      "debate-" + agent.ID,
			Role:    agent.Role,
			AgentID: agent.ID,
			Command: cloneStringSlice(agent.Command),
			CWD:     agent.CWD,
		})
	}
	return transport.ExecutionPlanPayload{
		Session: requestID,
		Layout:  "debate",
		Tabs: []transport.ExecutionPlanTab{
			{
				Name:  requestID,
				Panes: panes,
			},
		},
	}
}

func debateRoleCommand(bin string) []string {
	if strings.TrimSpace(bin) != "" {
		return []string{bin, "role"}
	}
	exe, err := os.Executable()
	if err == nil && exe != "" {
		return []string{exe, "role"}
	}
	return []string{"zellij-agent", "role"}
}

func debateMarkers(requestID string, agents []string, round int) map[string]string {
	markers := make(map[string]string, len(agents))
	for _, agent := range agents {
		paneID := "debate-" + agent
		markers[paneID] = debateCompletionMarker(requestID, round, agent)
	}
	return markers
}

func debateCompletionMarker(requestID string, round int, agent string) string {
	return fmt.Sprintf("<<<AGENT_DEBATE_DONE debate=%s round=%d agent=%s token=%s-%d-%s>>>", requestID, round, agent, requestID, round, agent)
}

func debateRoundPrompt(topic string, round int, agent string, previousRounds []debateRoundOutput, marker string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Round: %d
Agent: %s
Topic: %s

`, round, agent, topic)
	if len(previousRounds) == 0 {
		fmt.Fprintln(&b, "Think independently and give your best answer for this round.")
	} else {
		fmt.Fprintln(&b, "Review the previous round answers below, then refine your position for this round.")
		fmt.Fprintln(&b, "Address strong opposing points, update weak assumptions, and keep your answer focused.")
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Previous round answers:")
		appendDebateRoundOutputs(&b, previousRounds, nil)
	}
	fmt.Fprintf(&b, `
When your answer is complete, print the completion marker on its own final line.

Completion marker parts:
Print these parts concatenated exactly, with no extra spaces, as your final line:
%s
`, formatDebateMarkerParts(marker))
	return b.String()
}

func formatDebateMarkerParts(marker string) string {
	parts := debateMarkerParts(marker)
	var b strings.Builder
	for i, part := range parts {
		fmt.Fprintf(&b, "%d. %s\n", i+1, part)
	}
	return b.String()
}

func debateMarkerParts(marker string) []string {
	body := strings.TrimPrefix(marker, "<<<AGENT_DEBATE_DONE")
	body = strings.TrimSuffix(body, ">>>")
	fields := strings.Fields(body)
	parts := []string{"<<<AGENT_DEBATE_DONE"}
	for _, field := range fields {
		parts = append(parts, " "+field)
	}
	parts = append(parts, ">>>")
	return parts
}

func debateSynthesisBlock(topic string, agents []string, rounds []debateRoundOutput, marker string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "<<<DEBATE_SYNTHESIS_BEGIN>>>")
	fmt.Fprintf(&b, "Completion-Marker-Base64: %s\n", base64.StdEncoding.EncodeToString([]byte(marker)))
	fmt.Fprintf(&b, "Topic: %s\n\n", topic)
	fmt.Fprintln(&b, "You are the debate coordinator. Read all agent answers below and produce a concise synthesis.")
	fmt.Fprintln(&b, "Include consensus, disagreements, strongest arguments, weak assumptions, and a final recommendation.")
	fmt.Fprintln(&b)
	appendDebateRoundOutputs(&b, rounds, agents)
	fmt.Fprintln(&b, "<<<DEBATE_SYNTHESIS_END>>>")
	return b.String()
}

func appendDebateRoundOutputs(b *strings.Builder, rounds []debateRoundOutput, agents []string) {
	for _, roundOutput := range rounds {
		roundAgents := agents
		if len(roundAgents) == 0 {
			roundAgents = sortedDebatePaneAgents(roundOutput.Outputs)
		}
		for _, agent := range roundAgents {
			paneID := agent
			if !strings.HasPrefix(paneID, "debate-") {
				paneID = "debate-" + agent
			}
			fmt.Fprintf(b, "[round %d %s]%s\n%s\n\n", roundOutput.Round, paneID, debateStatusSuffix(roundOutput.Statuses[paneID]), roundOutput.Outputs[paneID])
		}
	}
}

func debateStatusSuffix(status debatePaneWaitStatus) string {
	if status.Status == "" {
		return ""
	}
	return fmt.Sprintf(" status=%s elapsed=%s timeout=%s", status.Status, status.Elapsed.Round(time.Millisecond), status.Timeout)
}

func printDebateStatus(w io.Writer, rounds []debateRoundOutput, agents []string) {
	if len(rounds) == 0 {
		return
	}
	fmt.Fprintln(w, "\n[debate status]")
	for _, roundOutput := range rounds {
		for _, agent := range agents {
			paneID := "debate-" + agent
			status := roundOutput.Statuses[paneID]
			if status.Status == "" {
				status = debatePaneWaitStatus{PaneID: paneID, Status: debatePaneStatusTimedOut}
			}
			fmt.Fprintf(w, "round=%d pane=%s status=%s elapsed=%s timeout=%s\n",
				roundOutput.Round,
				paneID,
				status.Status,
				status.Elapsed.Round(time.Millisecond),
				status.Timeout,
			)
		}
	}
}

func sortedDebatePaneAgents(outputs map[string]string) []string {
	agents := make([]string, 0, len(outputs))
	for paneID := range outputs {
		agents = append(agents, strings.TrimPrefix(paneID, "debate-"))
	}
	sort.Strings(agents)
	return agents
}

func waitForDebateMarkers(ctx context.Context, client AgentClient, markers map[string]string, agentTimeout time.Duration) (map[string]debatePaneWaitStatus, error) {
	stream, err := client.StreamEvents(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	started := time.Now()
	deadlines := make(map[string]time.Time, len(markers))
	statuses := make(map[string]debatePaneWaitStatus, len(markers))
	for paneID := range markers {
		deadlines[paneID] = started.Add(agentTimeout)
	}
	events := stream.Events
	errs := stream.Errors
	for len(statuses) < len(markers) {
		nextDeadline, ok := nextDebateDeadline(markers, statuses, deadlines)
		if !ok {
			break
		}
		wait := time.Until(nextDeadline)
		if wait <= 0 {
			markDebateTimeouts(markers, statuses, deadlines, started, agentTimeout, time.Now())
			continue
		}
		select {
		case event, ok := <-events:
			if !ok {
				if len(statuses) == len(markers) {
					return statuses, nil
				}
				return nil, fmt.Errorf("event stream closed before markers arrived; missing=%s", strings.Join(missingDebateMarkers(markers, statuses), ","))
			}
			if event.Type != "raw_output" {
				continue
			}
			marker, ok := markers[event.PaneID]
			if ok && strings.Contains(event.Message, marker) {
				if _, alreadyRecorded := statuses[event.PaneID]; !alreadyRecorded {
					statuses[event.PaneID] = debatePaneWaitStatus{
						PaneID:  event.PaneID,
						Status:  debatePaneStatusDone,
						Elapsed: time.Since(started),
						Timeout: agentTimeout,
					}
				}
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				return nil, err
			}
		case <-time.After(wait):
			markDebateTimeouts(markers, statuses, deadlines, started, agentTimeout, time.Now())
		case <-ctx.Done():
			return nil, fmt.Errorf("%w; missing=%s", ctx.Err(), strings.Join(missingDebateMarkers(markers, statuses), ","))
		}
	}
	return statuses, nil
}

func nextDebateDeadline(markers map[string]string, statuses map[string]debatePaneWaitStatus, deadlines map[string]time.Time) (time.Time, bool) {
	var next time.Time
	for paneID := range markers {
		if _, ok := statuses[paneID]; ok {
			continue
		}
		deadline := deadlines[paneID]
		if next.IsZero() || deadline.Before(next) {
			next = deadline
		}
	}
	if next.IsZero() {
		return time.Time{}, false
	}
	return next, true
}

func markDebateTimeouts(markers map[string]string, statuses map[string]debatePaneWaitStatus, deadlines map[string]time.Time, started time.Time, agentTimeout time.Duration, now time.Time) {
	for paneID := range markers {
		if _, ok := statuses[paneID]; ok {
			continue
		}
		deadline := deadlines[paneID]
		if deadline.After(now) {
			continue
		}
		statuses[paneID] = debatePaneWaitStatus{
			PaneID:  paneID,
			Status:  debatePaneStatusTimedOut,
			Elapsed: now.Sub(started),
			Timeout: agentTimeout,
		}
	}
}

func missingDebateMarkers(markers map[string]string, statuses map[string]debatePaneWaitStatus) []string {
	missing := make([]string, 0, len(markers))
	for paneID := range markers {
		if _, ok := statuses[paneID]; !ok {
			missing = append(missing, paneID)
		}
	}
	sort.Strings(missing)
	return missing
}

func cloneStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
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
