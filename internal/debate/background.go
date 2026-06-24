package debate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"zellij-with-codeagent/internal/transport"
)

type BackgroundOptions struct {
	Topic        string
	Agents       []string
	Rounds       int
	AgentTimeout time.Duration
	ConfigPath   string
	CWD          string
	Runner       BackgroundCommandRunner
	Progress     io.Writer
}

type BackgroundCommandRequest struct {
	AgentID string
	Command []string
	CWD     string
	Stdin   string
}

type BackgroundCommandResult struct {
	Stdout string
	Stderr string
}

type BackgroundCommandRunner interface {
	Run(context.Context, BackgroundCommandRequest) (BackgroundCommandResult, error)
}

type ExecBackgroundRunner struct{}

var defaultBackgroundRunner BackgroundCommandRunner = ExecBackgroundRunner{}

func SetBackgroundRunnerForTesting(runner BackgroundCommandRunner) func() {
	previous := defaultBackgroundRunner
	defaultBackgroundRunner = runner
	return func() {
		defaultBackgroundRunner = previous
	}
}

func (ExecBackgroundRunner) Run(ctx context.Context, req BackgroundCommandRequest) (BackgroundCommandResult, error) {
	if len(req.Command) == 0 {
		return BackgroundCommandResult{}, errors.New("background command is required")
	}
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	if req.CWD != "" {
		cmd.Dir = req.CWD
	}
	if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return BackgroundCommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

type backgroundAgentResult struct {
	paneID string
	output string
	status paneWaitStatus
}

func RunBackground(ctx context.Context, opts BackgroundOptions) (Result, error) {
	if strings.TrimSpace(opts.Topic) == "" {
		return Result{}, ValidationError{Message: "debate-background requires --topic <text>"}
	}
	if len(opts.Agents) == 0 {
		return Result{}, ValidationError{Message: "debate-background requires at least one agent"}
	}
	if opts.Rounds < 1 || opts.Rounds > 3 {
		return Result{}, ValidationError{Message: "debate-background requires --rounds between 1 and 3"}
	}
	if opts.AgentTimeout <= 0 {
		return Result{}, ValidationError{Message: "debate-background requires --agent-timeout greater than 0"}
	}

	agentSpecs := defaultAgentSpecs(opts.Agents, opts.CWD, nil)
	agents := cloneStringSlice(opts.Agents)
	if strings.TrimSpace(opts.ConfigPath) != "" {
		loadedAgents, _, err := loadConfig(opts.ConfigPath, opts.CWD, coordinatorSpec{})
		if err != nil {
			return Result{}, ValidationError{Message: fmt.Sprintf("debate-background config failed: %v", err)}
		}
		agentSpecs = loadedAgents
		agents = agentIDs(agentSpecs)
	}
	if err := validateBackgroundAgentSpecs(agentSpecs); err != nil {
		return Result{}, err
	}

	runner := opts.Runner
	if runner == nil {
		runner = defaultBackgroundRunner
	}

	requestID := fmt.Sprintf("debate_%d", time.Now().UnixNano())
	roundOutputs := make([]roundOutput, 0, opts.Rounds)
	for round := 1; round <= opts.Rounds; round++ {
		printBackgroundProgress(opts.Progress, "round=%d/%d status=started agents=%s", round, opts.Rounds, strings.Join(agents, ","))
		markers := markers(requestID, agents, round)
		results := runBackgroundRound(ctx, runner, opts.Topic, round, agents, agentSpecs, roundOutputs, markers, opts.AgentTimeout)
		outputs := make(map[string]string, len(results))
		statuses := make(map[string]paneWaitStatus, len(results))
		for _, result := range results {
			outputs[result.paneID] = result.output
			statuses[result.paneID] = result.status
		}
		roundOutputs = append(roundOutputs, roundOutput{Round: round, Outputs: outputs, Statuses: statuses})
		printBackgroundProgress(opts.Progress, "round=%d/%d status=done", round, opts.Rounds)
	}

	coordinatorMarker := completionMarker(requestID, opts.Rounds, "coordinator")
	coordinatorReq := BackgroundCommandRequest{
		AgentID: "debate-coordinator",
		Command: defaultCoordinatorPrintCommand(opts.CWD),
		CWD:     opts.CWD,
		Stdin:   synthesisBlock(opts.Topic, agents, roundOutputs, coordinatorMarker),
	}
	printBackgroundProgress(opts.Progress, "coordinator status=started")
	coordinatorCtx, cancel := context.WithTimeout(ctx, opts.AgentTimeout)
	defer cancel()
	coordinatorOutput, err := runner.Run(coordinatorCtx, coordinatorReq)
	if err != nil {
		return Result{}, fmt.Errorf("zellij-agent debate-background coordinator failed: %w%s", err, commandFailureDetail(coordinatorOutput))
	}
	printBackgroundProgress(opts.Progress, "coordinator status=done")

	return Result{
		response: transport.ExecutionPlanResponse{
			RequestID: requestID,
			Session:   requestID,
			Layout:    "debate-background",
		},
		agents:            agents,
		roundOutputs:      roundOutputs,
		coordinatorOutput: strings.TrimRight(coordinatorOutput.Stdout, "\n"),
	}, nil
}

func printBackgroundProgress(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "[debate progress] "+format+"\n", args...)
}

func validateBackgroundAgentSpecs(agents []agentSpec) error {
	for _, agent := range agents {
		if len(agent.PrintCommand) == 0 {
			return ValidationError{Message: fmt.Sprintf("debate-background agent %s requires print_command", agent.ID)}
		}
		if agent.PromptDelivery == "" {
			continue
		}
		if agent.PromptDelivery != promptDeliveryArg && agent.PromptDelivery != promptDeliveryStdin {
			return ValidationError{Message: fmt.Sprintf("debate-background agent %s prompt_delivery must be %q or %q", agent.ID, promptDeliveryArg, promptDeliveryStdin)}
		}
	}
	return nil
}

func runBackgroundRound(ctx context.Context, runner BackgroundCommandRunner, topic string, round int, agents []string, specs []agentSpec, previous []roundOutput, markers map[string]string, timeout time.Duration) []backgroundAgentResult {
	results := make(chan backgroundAgentResult, len(agents))
	var wg sync.WaitGroup
	for _, agent := range agents {
		agent := agent
		wg.Add(1)
		go func() {
			defer wg.Done()
			spec := agentSpecByID(specs, agent)
			paneID := "debate-" + agent
			started := time.Now()
			prompt := roundPrompt(topic, round, agent, previous, markers[paneID])
			req := backgroundCommandRequest(spec, prompt)
			req.AgentID = agent
			agentCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			commandOutput, err := runner.Run(agentCtx, req)
			status := paneStatusDone
			if err != nil {
				status = paneStatusFailed
				if errors.Is(agentCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
					status = paneStatusTimedOut
				}
			}
			results <- backgroundAgentResult{
				paneID: paneID,
				output: commandOutputText(commandOutput, err),
				status: paneWaitStatus{
					PaneID:  paneID,
					Status:  status,
					Elapsed: time.Since(started),
					Timeout: timeout,
				},
			}
		}()
	}
	wg.Wait()
	close(results)

	byPane := make(map[string]backgroundAgentResult, len(agents))
	for result := range results {
		byPane[result.paneID] = result
	}
	ordered := make([]backgroundAgentResult, 0, len(agents))
	for _, agent := range agents {
		ordered = append(ordered, byPane["debate-"+agent])
	}
	return ordered
}

func backgroundCommandRequest(spec agentSpec, prompt string) BackgroundCommandRequest {
	req := BackgroundCommandRequest{
		Command: cloneStringSlice(spec.PrintCommand),
		CWD:     spec.CWD,
	}
	switch spec.PromptDelivery {
	case promptDeliveryArg:
		req.Command = append(req.Command, prompt)
	default:
		req.Stdin = prompt
	}
	return req
}

func defaultCoordinatorPrintCommand(cwd string) []string {
	return []string{"codex", "exec", "--cd", cwd, "-"}
}

func commandOutputText(result BackgroundCommandResult, err error) string {
	var parts []string
	if strings.TrimSpace(result.Stdout) != "" {
		parts = append(parts, strings.TrimRight(result.Stdout, "\n"))
	}
	if strings.TrimSpace(result.Stderr) != "" {
		parts = append(parts, "[stderr]\n"+strings.TrimRight(result.Stderr, "\n"))
	}
	if err != nil && len(parts) == 0 {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "\n")
}

func commandFailureDetail(result BackgroundCommandResult) string {
	text := commandOutputText(result, nil)
	if text == "" {
		return ""
	}
	return ": " + text
}
