package debate

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"zellij-with-codeagent/internal/transport"
)

type Client interface {
	SendInput(context.Context, string, transport.SendInputRequest) error
	SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error)
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
}

type Options struct {
	Topic        string
	Agents       []string
	Rounds       int
	AgentTimeout time.Duration
	ConfigPath   string
	CWD          string
	AgentRoleBin string
}

type Result struct {
	response          transport.ExecutionPlanResponse
	agents            []string
	roundOutputs      []roundOutput
	coordinatorOutput string
}

type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func IsValidationError(err error) bool {
	var validationErr ValidationError
	return errors.As(err, &validationErr)
}

func Run(ctx context.Context, client Client, opts Options) (Result, error) {
	if strings.TrimSpace(opts.Topic) == "" {
		return Result{}, ValidationError{Message: "debate requires --topic <text>"}
	}
	if len(opts.Agents) == 0 {
		return Result{}, ValidationError{Message: "debate requires at least one agent"}
	}
	if opts.Rounds < 1 || opts.Rounds > 3 {
		return Result{}, ValidationError{Message: "debate requires --rounds between 1 and 3"}
	}
	if opts.AgentTimeout <= 0 {
		return Result{}, ValidationError{Message: "debate requires --agent-timeout greater than 0"}
	}

	roleCommand := roleCommand(opts.AgentRoleBin)
	agentSpecs := defaultAgentSpecs(opts.Agents, opts.CWD, roleCommand)
	coordinatorSpec := defaultCoordinatorSpec(opts.CWD, roleCommand)
	agents := cloneStringSlice(opts.Agents)
	if strings.TrimSpace(opts.ConfigPath) != "" {
		loadedAgents, loadedCoordinator, err := loadConfig(opts.ConfigPath, opts.CWD, coordinatorSpec)
		if err != nil {
			return Result{}, ValidationError{Message: fmt.Sprintf("debate config failed: %v", err)}
		}
		agentSpecs = loadedAgents
		coordinatorSpec = loadedCoordinator
		agents = agentIDs(agentSpecs)
	}

	requestID := fmt.Sprintf("debate_%d", time.Now().UnixNano())
	payload := executionPlan(requestID, agentSpecs, coordinatorSpec)
	response, err := client.SubmitExecutionPlan(ctx, requestID, payload)
	if err != nil {
		return Result{}, fmt.Errorf("agentctl debate plan failed: %w", err)
	}
	if err := waitForAgentStartup(ctx, agentSpecs); err != nil {
		return Result{}, fmt.Errorf("agentctl debate startup wait failed: %w", err)
	}

	roundOutputs := make([]roundOutput, 0, opts.Rounds)
	for round := 1; round <= opts.Rounds; round++ {
		markers := markers(requestID, agents, round)
		for _, agent := range agents {
			paneID := "debate-" + agent
			agentSpec := agentSpecByID(agentSpecs, agent)
			if err := sendAgentInput(ctx, client, paneID, roundPrompt(opts.Topic, round, agent, roundOutputs, markers[paneID]), agentSpec); err != nil {
				return Result{}, fmt.Errorf("agentctl debate prompt failed: %w", err)
			}
		}
		statuses, err := waitForMarkers(ctx, client, markers, opts.AgentTimeout)
		if err != nil {
			return Result{}, fmt.Errorf("agentctl debate wait failed: %w", err)
		}
		agentOutputs := make(map[string]string, len(agents))
		for _, agent := range agents {
			paneID := "debate-" + agent
			snapshot, err := client.SnapshotOutput(ctx, paneID, transport.SnapshotOutputRequest{Full: true})
			if err != nil {
				return Result{}, fmt.Errorf("agentctl debate snapshot failed: %w", err)
			}
			agentOutputs[paneID] = snapshot.Output
		}
		roundOutputs = append(roundOutputs, roundOutput{Round: round, Outputs: agentOutputs, Statuses: statuses})
	}

	coordinatorMarker := completionMarker(requestID, opts.Rounds, "coordinator")
	if err := client.SendInput(ctx, "debate-coordinator", transport.SendInputRequest{Text: synthesisBlock(opts.Topic, agents, roundOutputs, coordinatorMarker)}); err != nil {
		return Result{}, fmt.Errorf("agentctl debate synthesis prompt failed: %w", err)
	}
	coordinatorStatuses, err := waitForMarkers(ctx, client, map[string]string{"debate-coordinator": coordinatorMarker}, opts.AgentTimeout)
	if err != nil {
		return Result{}, fmt.Errorf("agentctl debate synthesis wait failed: %w", err)
	}
	if status := coordinatorStatuses["debate-coordinator"]; status.Status != paneStatusDone {
		return Result{}, fmt.Errorf("agentctl debate synthesis wait failed: debate-coordinator %s after %s", status.Status, status.Timeout)
	}
	coordinatorSnapshot, err := client.SnapshotOutput(ctx, "debate-coordinator", transport.SnapshotOutputRequest{Full: true})
	if err != nil {
		return Result{}, fmt.Errorf("agentctl debate coordinator snapshot failed: %w", err)
	}

	return Result{
		response:          response,
		agents:            agents,
		roundOutputs:      roundOutputs,
		coordinatorOutput: coordinatorSnapshot.Output,
	}, nil
}

func PrintResult(w io.Writer, result Result) {
	fmt.Fprintf(w, "debate request=%s session=%s agents=%s\n", result.response.RequestID, result.response.Session, strings.Join(result.agents, ","))
	printStatus(w, result.roundOutputs, result.agents)
	for _, roundOutput := range result.roundOutputs {
		for _, agent := range result.agents {
			paneID := "debate-" + agent
			fmt.Fprintf(w, "\n[round %d %s]\n%s\n", roundOutput.Round, paneID, roundOutput.Outputs[paneID])
		}
	}
	fmt.Fprintf(w, "\n[debate-coordinator synthesis]\n%s\n", result.coordinatorOutput)
}

type roundOutput struct {
	Round    int
	Outputs  map[string]string
	Statuses map[string]paneWaitStatus
}

const (
	paneStatusDone     = "done"
	paneStatusTimedOut = "timed_out"
	paneStatusFailed   = "failed"
)

const (
	promptDeliveryArg   = "arg"
	promptDeliveryStdin = "stdin"
)

type paneWaitStatus struct {
	PaneID  string
	Status  string
	Elapsed time.Duration
	Timeout time.Duration
}

type agentSpec struct {
	ID                 string
	Role               string
	Command            []string
	CWD                string
	StartupDelayMS     int
	SubmitNewlines     int
	ExtraSubmitEnters  int
	ExtraSubmitDelayMS int
	PrintCommand       []string
	PromptDelivery     string
}

type coordinatorSpec struct {
	Role    string
	Command []string
	CWD     string
}

type configFile struct {
	Agents      []agentConfig `yaml:"agents"`
	Coordinator paneConfig    `yaml:"coordinator"`
}

type agentConfig struct {
	ID                 string   `yaml:"id"`
	Role               string   `yaml:"role"`
	Command            []string `yaml:"command"`
	CWD                string   `yaml:"cwd"`
	SubmitNewlines     int      `yaml:"submit_newlines"`
	ExtraSubmitEnters  int      `yaml:"extra_submit_enters"`
	ExtraSubmitDelayMS int      `yaml:"extra_submit_delay_ms"`
	StartupDelayMS     int      `yaml:"startup_delay_ms"`
	PrintCommand       []string `yaml:"print_command"`
	PromptDelivery     string   `yaml:"prompt_delivery"`
}

type paneConfig struct {
	Role    string   `yaml:"role"`
	Command []string `yaml:"command"`
	CWD     string   `yaml:"cwd"`
}

func ParseAgents(raw string) []string {
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

func defaultAgentSpecs(agents []string, cwd string, roleCommand []string) []agentSpec {
	specs := make([]agentSpec, 0, len(agents))
	for _, agent := range agents {
		if spec, ok := defaultAgentCommandSpec(agent, cwd); ok {
			specs = append(specs, spec)
			continue
		}
		specs = append(specs, fallbackAgentSpec(agent, cwd, roleCommand))
	}
	return specs
}

func defaultAgentCommandSpec(agent string, cwd string) (agentSpec, bool) {
	specs := map[string]agentSpec{
		"agy": {
			ID:             "agy",
			Role:           "coding-agent",
			Command:        []string{"agy", "--dangerously-skip-permissions"},
			CWD:            cwd,
			StartupDelayMS: 10000,
			SubmitNewlines: 1,
			PrintCommand:   []string{"agy", "--dangerously-skip-permissions", "--print"},
			PromptDelivery: promptDeliveryArg,
		},
		"agent": {
			ID:                 "agent",
			Role:               "coding-agent",
			Command:            []string{"agent", "--yolo", "--model", "claude-opus-4-8-thinking-high"},
			CWD:                cwd,
			SubmitNewlines:     2,
			ExtraSubmitEnters:  1,
			ExtraSubmitDelayMS: 300,
			PrintCommand:       []string{"agent", "--yolo", "--print", "--model", "claude-opus-4-8-thinking-high", "--trust"},
			PromptDelivery:     promptDeliveryArg,
		},
		"codex": {
			ID:             "codex",
			Role:           "coding-agent",
			Command:        []string{"codex", "--dangerously-bypass-approvals-and-sandbox"},
			CWD:            cwd,
			SubmitNewlines: 1,
			PrintCommand:   []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "--cd", cwd, "-"},
			PromptDelivery: promptDeliveryStdin,
		},
		"a": {
			ID:             "a",
			Role:           "coding-agent",
			Command:        []string{"agy", "--dangerously-skip-permissions"},
			CWD:            cwd,
			StartupDelayMS: 10000,
			SubmitNewlines: 1,
			PrintCommand:   []string{"agy", "--dangerously-skip-permissions", "--print"},
			PromptDelivery: promptDeliveryArg,
		},
		"b": {
			ID:                 "b",
			Role:               "coding-agent",
			Command:            []string{"agent", "--yolo", "--model", "claude-opus-4-8-thinking-high"},
			CWD:                cwd,
			SubmitNewlines:     2,
			ExtraSubmitEnters:  1,
			ExtraSubmitDelayMS: 300,
			PrintCommand:       []string{"agent", "--yolo", "--print", "--model", "claude-opus-4-8-thinking-high", "--trust"},
			PromptDelivery:     promptDeliveryArg,
		},
		"c": {
			ID:             "c",
			Role:           "coding-agent",
			Command:        []string{"codex", "--dangerously-bypass-approvals-and-sandbox"},
			CWD:            cwd,
			SubmitNewlines: 1,
			PrintCommand:   []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "--cd", cwd, "-"},
			PromptDelivery: promptDeliveryStdin,
		},
	}
	spec, ok := specs[agent]
	if !ok {
		return agentSpec{}, false
	}
	spec.Command = cloneStringSlice(spec.Command)
	return spec, true
}

func fallbackAgentSpec(agent string, cwd string, roleCommand []string) agentSpec {
	return agentSpec{
		ID:             agent,
		Role:           "coding-agent",
		Command:        append(cloneStringSlice(roleCommand), "coding-agent", cwd),
		CWD:            cwd,
		SubmitNewlines: 1,
	}
}

func defaultCoordinatorSpec(cwd string, roleCommand []string) coordinatorSpec {
	return coordinatorSpec{
		Role:    "debate-coordinator",
		Command: append(cloneStringSlice(roleCommand), "debate-coordinator", cwd),
		CWD:     cwd,
	}
}

func loadConfig(path string, defaultCWD string, defaultCoordinator coordinatorSpec) ([]agentSpec, coordinatorSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, coordinatorSpec{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, coordinatorSpec{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Agents) == 0 {
		return nil, coordinatorSpec{}, fmt.Errorf("agents are required")
	}
	seen := make(map[string]struct{}, len(cfg.Agents))
	agents := make([]agentSpec, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		id := strings.TrimSpace(agent.ID)
		if id == "" {
			return nil, coordinatorSpec{}, fmt.Errorf("agent id is required")
		}
		if _, ok := seen[id]; ok {
			return nil, coordinatorSpec{}, fmt.Errorf("duplicate agent id %q", id)
		}
		seen[id] = struct{}{}
		if len(agent.Command) == 0 {
			return nil, coordinatorSpec{}, fmt.Errorf("agent %s command is required", id)
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
			return nil, coordinatorSpec{}, fmt.Errorf("agent %s submit_newlines must be at least 1", id)
		}
		if agent.ExtraSubmitEnters < 0 {
			return nil, coordinatorSpec{}, fmt.Errorf("agent %s extra_submit_enters must not be negative", id)
		}
		if agent.ExtraSubmitDelayMS < 0 {
			return nil, coordinatorSpec{}, fmt.Errorf("agent %s extra_submit_delay_ms must not be negative", id)
		}
		if agent.StartupDelayMS < 0 {
			return nil, coordinatorSpec{}, fmt.Errorf("agent %s startup_delay_ms must not be negative", id)
		}
		promptDelivery := strings.TrimSpace(agent.PromptDelivery)
		if promptDelivery != "" && promptDelivery != promptDeliveryArg && promptDelivery != promptDeliveryStdin {
			return nil, coordinatorSpec{}, fmt.Errorf("agent %s prompt_delivery must be %q or %q", id, promptDeliveryArg, promptDeliveryStdin)
		}
		agents = append(agents, agentSpec{
			ID:                 id,
			Role:               role,
			Command:            cloneStringSlice(agent.Command),
			CWD:                cwd,
			StartupDelayMS:     agent.StartupDelayMS,
			SubmitNewlines:     submitNewlines,
			ExtraSubmitEnters:  agent.ExtraSubmitEnters,
			ExtraSubmitDelayMS: agent.ExtraSubmitDelayMS,
			PrintCommand:       cloneStringSlice(agent.PrintCommand),
			PromptDelivery:     promptDelivery,
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

func agentIDs(agents []agentSpec) []string {
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		ids = append(ids, agent.ID)
	}
	return ids
}

func agentSpecByID(agents []agentSpec, id string) agentSpec {
	for _, agent := range agents {
		if agent.ID == id {
			return agent
		}
	}
	return agentSpec{ID: id, SubmitNewlines: 1}
}

func waitForAgentStartup(ctx context.Context, agents []agentSpec) error {
	var maxDelay time.Duration
	for _, agent := range agents {
		delay := time.Duration(agent.StartupDelayMS) * time.Millisecond
		if delay > maxDelay {
			maxDelay = delay
		}
	}
	return delay(ctx, maxDelay)
}

func sendAgentInput(ctx context.Context, client Client, paneID string, prompt string, agent agentSpec) error {
	if err := client.SendInput(ctx, paneID, transport.SendInputRequest{Text: withTrailingNewlines(prompt, agent.SubmitNewlines)}); err != nil {
		return err
	}
	for i := 0; i < agent.ExtraSubmitEnters; i++ {
		if agent.ExtraSubmitDelayMS > 0 {
			if err := delay(ctx, time.Duration(agent.ExtraSubmitDelayMS)*time.Millisecond); err != nil {
				return err
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

var delay = defaultDelay

func defaultDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func SetDelayForTesting(fn func(context.Context, time.Duration) error) func() {
	previous := delay
	delay = fn
	return func() {
		delay = previous
	}
}

func executionPlan(requestID string, agents []agentSpec, coordinator coordinatorSpec) transport.ExecutionPlanPayload {
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

func roleCommand(bin string) []string {
	if strings.TrimSpace(bin) != "" {
		return []string{bin, "role"}
	}
	exe, err := os.Executable()
	if err == nil && exe != "" {
		return []string{exe, "role"}
	}
	return []string{"zellij-agent", "role"}
}

func markers(requestID string, agents []string, round int) map[string]string {
	markers := make(map[string]string, len(agents))
	for _, agent := range agents {
		paneID := "debate-" + agent
		markers[paneID] = completionMarker(requestID, round, agent)
	}
	return markers
}

func completionMarker(requestID string, round int, agent string) string {
	return fmt.Sprintf("<<<AGENT_DEBATE_DONE debate=%s round=%d agent=%s token=%s-%d-%s>>>", requestID, round, agent, requestID, round, agent)
}

func roundPrompt(topic string, round int, agent string, previousRounds []roundOutput, marker string) string {
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
		appendRoundOutputs(&b, previousRounds, nil)
	}
	fmt.Fprintf(&b, `
When your answer is complete, print the completion marker on its own final line.

Completion marker parts:
Print these parts concatenated exactly as your final line.
Preserve every character shown in the parts, including leading spaces before debate/round/agent/token.
%s
`, formatMarkerParts(marker))
	return b.String()
}

func formatMarkerParts(marker string) string {
	parts := markerParts(marker)
	var b strings.Builder
	for i, part := range parts {
		fmt.Fprintf(&b, "%d. %s\n", i+1, part)
	}
	return b.String()
}

func markerParts(marker string) []string {
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

func synthesisBlock(topic string, agents []string, rounds []roundOutput, marker string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "<<<DEBATE_SYNTHESIS_BEGIN>>>")
	fmt.Fprintf(&b, "Completion-Marker-Base64: %s\n", base64.StdEncoding.EncodeToString([]byte(marker)))
	fmt.Fprintf(&b, "Topic: %s\n\n", topic)
	fmt.Fprintln(&b, "You are the debate coordinator. Read all agent answers below and produce a concise synthesis.")
	fmt.Fprintln(&b, "Include consensus, disagreements, strongest arguments, weak assumptions, and a final recommendation.")
	fmt.Fprintln(&b)
	appendRoundOutputs(&b, rounds, agents)
	fmt.Fprintln(&b, "<<<DEBATE_SYNTHESIS_END>>>")
	return b.String()
}

func appendRoundOutputs(b *strings.Builder, rounds []roundOutput, agents []string) {
	for _, roundOutput := range rounds {
		roundAgents := agents
		if len(roundAgents) == 0 {
			roundAgents = sortedPaneAgents(roundOutput.Outputs)
		}
		for _, agent := range roundAgents {
			paneID := agent
			if !strings.HasPrefix(paneID, "debate-") {
				paneID = "debate-" + agent
			}
			fmt.Fprintf(b, "[round %d %s]%s\n%s\n\n", roundOutput.Round, paneID, statusSuffix(roundOutput.Statuses[paneID]), roundOutput.Outputs[paneID])
		}
	}
}

func statusSuffix(status paneWaitStatus) string {
	if status.Status == "" {
		return ""
	}
	return fmt.Sprintf(" status=%s elapsed=%s timeout=%s", status.Status, status.Elapsed.Round(time.Millisecond), status.Timeout)
}

func printStatus(w io.Writer, rounds []roundOutput, agents []string) {
	if len(rounds) == 0 {
		return
	}
	fmt.Fprintln(w, "\n[debate status]")
	for _, roundOutput := range rounds {
		for _, agent := range agents {
			paneID := "debate-" + agent
			status := roundOutput.Statuses[paneID]
			if status.Status == "" {
				status = paneWaitStatus{PaneID: paneID, Status: paneStatusTimedOut}
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

func sortedPaneAgents(outputs map[string]string) []string {
	agents := make([]string, 0, len(outputs))
	for paneID := range outputs {
		agents = append(agents, strings.TrimPrefix(paneID, "debate-"))
	}
	sort.Strings(agents)
	return agents
}

func waitForMarkers(ctx context.Context, client Client, markers map[string]string, agentTimeout time.Duration) (map[string]paneWaitStatus, error) {
	stream, err := client.StreamEvents(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	started := time.Now()
	deadlines := make(map[string]time.Time, len(markers))
	statuses := make(map[string]paneWaitStatus, len(markers))
	for paneID := range markers {
		deadlines[paneID] = started.Add(agentTimeout)
	}
	events := stream.Events
	errs := stream.Errors
	for len(statuses) < len(markers) {
		nextDeadline, ok := nextDeadline(markers, statuses, deadlines)
		if !ok {
			break
		}
		wait := time.Until(nextDeadline)
		if wait <= 0 {
			markTimeouts(markers, statuses, deadlines, started, agentTimeout, time.Now())
			continue
		}
		select {
		case event, ok := <-events:
			if !ok {
				if len(statuses) == len(markers) {
					return statuses, nil
				}
				return nil, fmt.Errorf("event stream closed before markers arrived; missing=%s", strings.Join(missingMarkers(markers, statuses), ","))
			}
			if event.Type != "raw_output" {
				continue
			}
			marker, ok := markers[event.PaneID]
			if ok && markerMatches(event.Message, marker) {
				if _, alreadyRecorded := statuses[event.PaneID]; !alreadyRecorded {
					statuses[event.PaneID] = paneWaitStatus{
						PaneID:  event.PaneID,
						Status:  paneStatusDone,
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
			markTimeouts(markers, statuses, deadlines, started, agentTimeout, time.Now())
		case <-ctx.Done():
			return nil, fmt.Errorf("%w; missing=%s", ctx.Err(), strings.Join(missingMarkers(markers, statuses), ","))
		}
	}
	return statuses, nil
}

func markerMatches(message string, marker string) bool {
	return strings.Contains(message, marker) || strings.Contains(compactWhitespace(message), compactWhitespace(marker))
}

func compactWhitespace(value string) string {
	replacer := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "")
	return replacer.Replace(value)
}

func nextDeadline(markers map[string]string, statuses map[string]paneWaitStatus, deadlines map[string]time.Time) (time.Time, bool) {
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

func markTimeouts(markers map[string]string, statuses map[string]paneWaitStatus, deadlines map[string]time.Time, started time.Time, agentTimeout time.Duration, now time.Time) {
	for paneID := range markers {
		if _, ok := statuses[paneID]; ok {
			continue
		}
		deadline := deadlines[paneID]
		if deadline.After(now) {
			continue
		}
		statuses[paneID] = paneWaitStatus{
			PaneID:  paneID,
			Status:  paneStatusTimedOut,
			Elapsed: now.Sub(started),
			Timeout: agentTimeout,
		}
	}
}

func missingMarkers(markers map[string]string, statuses map[string]paneWaitStatus) []string {
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
