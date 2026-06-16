package plannercli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/planner"
	"zellij-with-codeagent/internal/transport"
)

type AgentClient interface {
	Health(context.Context) (transport.HealthResponse, error)
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
}

type ClientFactory func(socketPath string, timeout time.Duration) AgentClient

type Config struct {
	DefaultRoleCommand []string
}

func main() {
	os.Exit(RunWithInput(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, func(socketPath string, timeout time.Duration) AgentClient {
		return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
	}))
}

func Run(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	return RunWithConfig(args, stdout, stderr, newClient, Config{})
}

func RunWithConfig(args []string, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	return RunWithInputConfig(args, os.Stdin, stdout, stderr, newClient, cfg)
}

func RunWithInput(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory) int {
	return RunWithInputConfig(args, stdin, stdout, stderr, newClient, Config{})
}

func RunWithInputConfig(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "page":
		return runPage(args[1:], stdout, stderr, newClient, cfg)
	case "validate":
		return runValidate(args[1:], stdin, stdout, stderr)
	case "submit":
		return runSubmit(args[1:], stdin, stdout, stderr, newClient)
	case "tui":
		return runTUI(args[1:], stdin, stdout, stderr, newClient, cfg)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runValidate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	filePath := fs.String("file", "", "AI-generated /v1/requests execution_plan envelope file, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	plan, err := loadValidatedPlan(*filePath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "validate failed: %v\n", err)
		return 1
	}
	printValidationSummary(stdout, plan)
	return 0
}

func runSubmit(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory) int {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	filePath := fs.String("file", "", "AI-generated /v1/requests execution_plan envelope file, or - for stdin")
	showUI := fs.Bool("ui", false, "print planner status UI to stderr")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	plan, err := loadValidatedPlan(*filePath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "submit validation failed: %v\n", err)
		return 1
	}
	if *showUI {
		printEnvelopeUI(stderr, plan, "validated")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	response, err := newClient(*socketPath, *timeout).SubmitExecutionPlan(ctx, plan.Envelope.RequestID, plan.Payload)
	if err != nil {
		fmt.Fprintf(stderr, "submit execution plan failed: %v\n", err)
		return 1
	}
	if *showUI {
		printSubmitResult(stderr, response)
	}
	printExecutionPlanResponse(stdout, response)
	return 0
}

func runTUI(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	goal := fs.String("goal", "", "natural language planner request; include the URL in this text")
	targetURL := fs.String("url", "", "browser URL override; normally extracted from --goal or chat input")
	cwd := fs.String("cwd", defaultCWD(), "application working directory")
	mockSource := fs.String("mock-source", defaultMockSource(), "mock top-level source file for the URL")
	requestID := fs.String("request-id", "", "request id override")
	agentRoleBin := fs.String("agent-role-bin", "", "agent-role binary used by generated panes")
	dryRun := fs.Bool("dry-run", false, "print the /v1/requests envelope without submitting it")
	autoSubmit := fs.Bool("auto-submit", false, "submit without interactive confirmation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	health := checkAgentHealth(ctx, newClient(*socketPath, *timeout))

	reader := bufio.NewReader(stdin)
	fmt.Fprint(stderr, "\033[H\033[2J")
	fmt.Fprintln(stderr, "\033[1m\033[36m[AI PLANNER]\033[0m mock page planner")
	fmt.Fprintf(stderr, "agentd=%s socket=%s\n", health, *socketPath)
	fmt.Fprintf(stderr, "cwd=%s\n", *cwd)
	fmt.Fprintf(stderr, "mock_source=%s\n", *mockSource)
	fmt.Fprintln(stderr)

	var err error
	*goal, err = promptChat(reader, stderr, *goal)
	if err != nil {
		fmt.Fprintf(stderr, "read request failed: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*targetURL) == "" {
		extractedURL, ok := ExtractURL(*goal)
		if !ok {
			fmt.Fprintln(stderr, "request must include a URL, for example http://localhost:8000/example/aa")
			return 2
		}
		*targetURL = extractedURL
	}

	resolver := planner.MockSourceResolver{SourcePath: *mockSource}
	if *mockSource == defaultMockSource() {
		resolver.Reason = "built-in mock source"
	}
	resolved, err := resolver.ResolveSource(ctx, planner.ResolveSourceRequest{
		URL:  *targetURL,
		CWD:  *cwd,
		Goal: *goal,
	})
	if err != nil {
		fmt.Fprintf(stderr, "resolve source failed: %v\n", err)
		return 1
	}
	payload, err := planner.BuildPagePlan(planner.PagePlanRequest{
		URL:              *targetURL,
		CWD:              *cwd,
		AgentRoleBin:     pagePlanAgentRoleBin(*agentRoleBin, cfg),
		AgentRoleCommand: pagePlanAgentRoleCommand(*agentRoleBin, cfg),
	}, resolved)
	if err != nil {
		fmt.Fprintf(stderr, "build page plan failed: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*requestID) == "" {
		*requestID = "req_" + payload.Session
	}

	printPlannerUI(stderr, *requestID, resolved, payload, "mock-tui", submitStatus(*dryRun))
	fmt.Fprintf(stderr, "\nrequest_text=%s\n", *goal)

	envelope, err := executionPlanEnvelope(*requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "encode execution plan envelope: %v\n", err)
		return 1
	}
	if _, err := planner.ParseExecutionPlanEnvelope(mustMarshalEnvelope(envelope)); err != nil {
		fmt.Fprintf(stderr, "validate generated envelope failed: %v\n", err)
		return 1
	}

	if *dryRun {
		if err := writeEnvelope(stdout, envelope); err != nil {
			fmt.Fprintf(stderr, "write dry-run envelope: %v\n", err)
			return 1
		}
		return 0
	}

	if !*autoSubmit {
		confirmed, err := promptConfirm(reader, stderr, "Submit plan to agentd?")
		if err != nil {
			fmt.Fprintf(stderr, "read confirmation failed: %v\n", err)
			return 1
		}
		if !confirmed {
			fmt.Fprintln(stderr, "submit cancelled")
			if err := writeEnvelope(stdout, envelope); err != nil {
				fmt.Fprintf(stderr, "write envelope: %v\n", err)
				return 1
			}
			return 0
		}
	}

	response, err := newClient(*socketPath, *timeout).SubmitExecutionPlan(ctx, *requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "submit execution plan failed: %v\n", err)
		return 1
	}
	printSubmitResult(stderr, response)
	printExecutionPlanResponse(stdout, response)
	return 0
}

func runPage(args []string, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	fs := flag.NewFlagSet("page", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	targetURL := fs.String("url", "", "browser URL to inspect")
	cwd := fs.String("cwd", "", "application working directory")
	goal := fs.String("goal", "", "optional planner goal")
	session := fs.String("session", "", "execution session/task id override")
	requestID := fs.String("request-id", "", "request id override")
	mockSource := fs.String("mock-source", "", "mock top-level source file for the URL")
	agentRoleBin := fs.String("agent-role-bin", "", "agent-role binary used by generated panes")
	dryRun := fs.Bool("dry-run", false, "print the /v1/requests envelope without submitting it")
	showUI := fs.Bool("ui", false, "print planner status UI to stderr")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if strings.TrimSpace(*targetURL) == "" {
		fmt.Fprintln(stderr, "page requires --url")
		return 2
	}
	if strings.TrimSpace(*mockSource) == "" {
		*mockSource = defaultMockSource()
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	resolver := planner.MockSourceResolver{SourcePath: *mockSource}
	if *mockSource == defaultMockSource() {
		resolver.Reason = "built-in mock source"
	}
	resolved, err := resolver.ResolveSource(ctx, planner.ResolveSourceRequest{
		URL:  *targetURL,
		CWD:  *cwd,
		Goal: *goal,
	})
	if err != nil {
		fmt.Fprintf(stderr, "resolve source failed: %v\n", err)
		return 1
	}

	payload, err := planner.BuildPagePlan(planner.PagePlanRequest{
		URL:              *targetURL,
		CWD:              *cwd,
		Session:          *session,
		AgentRoleBin:     pagePlanAgentRoleBin(*agentRoleBin, cfg),
		AgentRoleCommand: pagePlanAgentRoleCommand(*agentRoleBin, cfg),
	}, resolved)
	if err != nil {
		fmt.Fprintf(stderr, "build page plan failed: %v\n", err)
		return 1
	}

	resolvedRequestID := strings.TrimSpace(*requestID)
	if resolvedRequestID == "" {
		resolvedRequestID = "req_" + payload.Session
	}

	if *showUI {
		printPlannerUI(stderr, resolvedRequestID, resolved, payload, "mock", submitStatus(*dryRun))
	}

	if *dryRun {
		envelope, err := executionPlanEnvelope(resolvedRequestID, payload)
		if err != nil {
			fmt.Fprintf(stderr, "encode execution plan envelope: %v\n", err)
			return 1
		}
		if err := writeEnvelope(stdout, envelope); err != nil {
			fmt.Fprintf(stderr, "write dry-run envelope: %v\n", err)
			return 1
		}
		return 0
	}

	response, err := newClient(*socketPath, *timeout).SubmitExecutionPlan(ctx, resolvedRequestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "submit execution plan failed: %v\n", err)
		return 1
	}
	if *showUI {
		printSubmitResult(stderr, response)
	}
	printExecutionPlanResponse(stdout, response)
	return 0
}

func loadValidatedPlan(filePath string, stdin io.Reader) (planner.ValidatedExecutionPlan, error) {
	if strings.TrimSpace(filePath) == "" {
		return planner.ValidatedExecutionPlan{}, errors.New("--file is required")
	}
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
	return planner.ParseExecutionPlanEnvelope(data)
}

func writeEnvelope(w io.Writer, envelope transport.RequestEnvelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

func mustMarshalEnvelope(envelope transport.RequestEnvelope) []byte {
	data, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return data
}

func executionPlanEnvelope(requestID string, payload transport.ExecutionPlanPayload) (transport.RequestEnvelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return transport.RequestEnvelope{}, err
	}
	return transport.RequestEnvelope{
		Type:      transport.RequestTypeExecutionPlan,
		RequestID: requestID,
		Payload:   raw,
	}, nil
}

func DefaultAgentRoleBin() string {
	path, err := exec.LookPath("agent-role")
	if err == nil {
		return path
	}
	return "agent-role"
}

func pagePlanAgentRoleBin(flagValue string, cfg Config) string {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue
	}
	if len(cfg.DefaultRoleCommand) != 0 {
		return ""
	}
	return DefaultAgentRoleBin()
}

func pagePlanAgentRoleCommand(flagValue string, cfg Config) []string {
	if strings.TrimSpace(flagValue) != "" || len(cfg.DefaultRoleCommand) == 0 {
		return nil
	}
	return append([]string(nil), cfg.DefaultRoleCommand...)
}

func defaultCWD() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func defaultMockSource() string {
	wd, err := os.Getwd()
	if err != nil {
		return "README.md"
	}
	return filepath.Join(wd, "README.md")
}

func checkAgentHealth(ctx context.Context, client AgentClient) string {
	response, err := client.Health(ctx)
	if err != nil {
		return "unreachable"
	}
	if response.Status == "" {
		return "ok"
	}
	if response.Version != "" {
		return response.Status + "(" + response.Version + ")"
	}
	return response.Status
}

func promptChat(reader *bufio.Reader, w io.Writer, current string) (string, error) {
	if strings.TrimSpace(current) != "" {
		fmt.Fprintf(w, "› %s\n", current)
		return current, nil
	}
	fmt.Fprint(w, "› ")
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("request is required")
	}
	return line, nil
}

func ExtractURL(text string) (string, bool) {
	match := requestURLPattern.FindString(text)
	if match == "" {
		return "", false
	}
	match = strings.TrimRight(match, ".,)]}\"'")
	if strings.HasPrefix(match, "http://") || strings.HasPrefix(match, "https://") {
		return match, true
	}
	return "http://" + match, true
}

var requestURLPattern = regexp.MustCompile(`https?://[^\s]+|(?:localhost|127\.0\.0\.1):[0-9]+[^\s]*`)

func promptRequired(reader *bufio.Reader, w io.Writer, label, current string) (string, error) {
	value, err := promptOptional(reader, w, label, current)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", strings.ToLower(label))
	}
	return value, nil
}

func promptOptional(reader *bufio.Reader, w io.Writer, label, current string) (string, error) {
	if current != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, current)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current, nil
	}
	return line, nil
}

func promptConfirm(reader *bufio.Reader, w io.Writer, label string) (bool, error) {
	fmt.Fprintf(w, "%s [y/N]: ", label)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func printValidationSummary(w io.Writer, plan planner.ValidatedExecutionPlan) {
	paneCount := 0
	for _, tab := range plan.Payload.Tabs {
		paneCount += len(tab.Panes)
	}
	fmt.Fprintf(w, "valid request=%s session=%s layout=%s tabs=%d panes=%d\n",
		plan.Envelope.RequestID,
		plan.Payload.Session,
		plan.Payload.Layout,
		len(plan.Payload.Tabs),
		paneCount,
	)
}

func printEnvelopeUI(w io.Writer, plan planner.ValidatedExecutionPlan, status string) {
	fmt.Fprintf(w, "\033[H\033[2J")
	fmt.Fprintf(w, "\033[1m\033[36m[AI PLANNER]\033[0m JSON execution plan\n")
	fmt.Fprintf(w, "status=%s request=%s\n", status, plan.Envelope.RequestID)
	fmt.Fprintf(w, "session=%s layout=%s\n", plan.Payload.Session, plan.Payload.Layout)
	for _, tab := range plan.Payload.Tabs {
		fmt.Fprintf(w, "tab=%s panes=%d\n", tab.Name, len(tab.Panes))
		for _, pane := range tab.Panes {
			fmt.Fprintf(w, "- %s role=%s\n", pane.ID, pane.Role)
		}
	}
}

func submitStatus(dryRun bool) string {
	if dryRun {
		return "dry-run"
	}
	return "ready"
}

func printPlannerUI(w io.Writer, requestID string, resolved planner.ResolveSourceResult, payload transport.ExecutionPlanPayload, mode, status string) {
	fmt.Fprintf(w, "\033[H\033[2J")
	fmt.Fprintf(w, "\033[1m\033[36m[AI PLANNER]\033[0m Page inspection plan\n")
	fmt.Fprintf(w, "mode=%s status=%s request=%s\n", mode, status, requestID)
	fmt.Fprintf(w, "url=%s\n", resolved.URL)
	fmt.Fprintf(w, "cwd=%s\n", resolved.CWD)
	fmt.Fprintf(w, "source=%s\n", resolved.SourcePath)
	if resolved.Reason != "" {
		fmt.Fprintf(w, "reason=%s\n", resolved.Reason)
	}
	fmt.Fprintf(w, "\nplan session=%s layout=%s\n", payload.Session, payload.Layout)
	for _, tab := range payload.Tabs {
		fmt.Fprintf(w, "tab=%s panes=%d\n", tab.Name, len(tab.Panes))
		for _, pane := range tab.Panes {
			fmt.Fprintf(w, "- %s role=%s\n", pane.ID, pane.Role)
		}
	}
}

func printSubmitResult(w io.Writer, response transport.ExecutionPlanResponse) {
	fmt.Fprintf(w, "\nsubmitted request=%s session=%s\n", response.RequestID, response.Session)
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

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-planner <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  page    Resolve a page source with mock data and submit a page inspection plan")
	fmt.Fprintln(w, "  tui     Interactive mock UI for natural-language page planning")
	fmt.Fprintln(w, "  validate")
	fmt.Fprintln(w, "          Validate an AI-generated /v1/requests execution_plan JSON file")
	fmt.Fprintln(w, "  submit  Validate and submit an AI-generated /v1/requests execution_plan JSON file")
}
