package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zellij-with-codeagent/internal/planner"
	"zellij-with-codeagent/internal/transport"
)

const defaultSocketPath = "/tmp/agentd.sock"

type agentClient interface {
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
}

type clientFactory func(socketPath string, timeout time.Duration) agentClient

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, func(socketPath string, timeout time.Duration) agentClient {
		return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
	}))
}

func run(args []string, stdout, stderr io.Writer, newClient clientFactory) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "page":
		return runPage(args[1:], stdout, stderr, newClient)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "submit":
		return runSubmit(args[1:], stdout, stderr, newClient)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runValidate(args []string, stdout, stderr io.Writer) int {
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

	plan, err := loadValidatedPlan(*filePath, os.Stdin)
	if err != nil {
		fmt.Fprintf(stderr, "validate failed: %v\n", err)
		return 1
	}
	printValidationSummary(stdout, plan)
	return 0
}

func runSubmit(args []string, stdout, stderr io.Writer, newClient clientFactory) int {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", defaultSocketPath, "agentd Unix socket path")
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

	plan, err := loadValidatedPlan(*filePath, os.Stdin)
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

func runPage(args []string, stdout, stderr io.Writer, newClient clientFactory) int {
	fs := flag.NewFlagSet("page", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", defaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	targetURL := fs.String("url", "", "browser URL to inspect")
	cwd := fs.String("cwd", "", "application working directory")
	goal := fs.String("goal", "", "optional planner goal")
	session := fs.String("session", "", "execution session/task id override")
	requestID := fs.String("request-id", "", "request id override")
	mockSource := fs.String("mock-source", "", "mock top-level source file for the URL")
	agentRoleBin := fs.String("agent-role-bin", defaultAgentRoleBin(), "agent-role binary used by generated panes")
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
		fmt.Fprintln(stderr, "page requires --mock-source")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	resolved, err := (planner.MockSourceResolver{SourcePath: *mockSource}).ResolveSource(ctx, planner.ResolveSourceRequest{
		URL:  *targetURL,
		CWD:  *cwd,
		Goal: *goal,
	})
	if err != nil {
		fmt.Fprintf(stderr, "resolve source failed: %v\n", err)
		return 1
	}

	payload, err := planner.BuildPagePlan(planner.PagePlanRequest{
		URL:          *targetURL,
		CWD:          *cwd,
		Session:      *session,
		AgentRoleBin: *agentRoleBin,
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
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(envelope); err != nil {
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

func defaultAgentRoleBin() string {
	wd, err := os.Getwd()
	if err != nil {
		return "agent-role"
	}
	return filepath.Join(wd, "bin", "agent-role")
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
	fmt.Fprintln(w, "  validate")
	fmt.Fprintln(w, "          Validate an AI-generated /v1/requests execution_plan JSON file")
	fmt.Fprintln(w, "  submit  Validate and submit an AI-generated /v1/requests execution_plan JSON file")
}
