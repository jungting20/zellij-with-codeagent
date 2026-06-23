package workcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/planner"
	"zellij-with-codeagent/internal/transport"
	workplan "zellij-with-codeagent/internal/work"
)

type AgentClient interface {
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
}

type ClientFactory func(socketPath string, timeout time.Duration) AgentClient

type Config struct {
	DefaultRoleCommand []string
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	_ = stdin

	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		printUsage(stdout)
		return 0
	}

	fs := flag.NewFlagSet("work", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	cwdFlag := fs.String("cwd", "", "application working directory")
	session := fs.String("session", "", "execution session/task id override")
	dryRun := fs.Bool("dry-run", false, "print the /v1/requests envelope without submitting it")
	autoTest := fs.Bool("auto-test", false, "run go test ./... in the test pane")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	goal := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if goal == "" {
		fmt.Fprintln(stderr, "work requires a goal")
		return 2
	}

	cwd, err := resolveCWD(*cwdFlag)
	if err != nil {
		fmt.Fprintf(stderr, "resolve cwd: %v\n", err)
		return 1
	}

	payload, err := workplan.BuildPlan(workplan.PlanRequest{
		Goal:        goal,
		CWD:         cwd,
		Session:     *session,
		RoleCommand: cfg.DefaultRoleCommand,
		AutoTest:    *autoTest,
	})
	if err != nil {
		fmt.Fprintf(stderr, "build work plan: %v\n", err)
		return 1
	}

	requestID := workplan.RequestID(payload.Session)
	envelope, err := executionPlanEnvelope(requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "encode execution plan envelope: %v\n", err)
		return 1
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		fmt.Fprintf(stderr, "encode execution plan envelope: %v\n", err)
		return 1
	}
	if _, err := planner.ParseExecutionPlanEnvelope(envelopeBytes); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	response, err := newClient(*socketPath, *timeout).SubmitExecutionPlan(ctx, requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "work submit failed via socket %s: %v\n", *socketPath, err)
		fmt.Fprintln(stderr, "hint: start the daemon with zellij-agent daemon serve")
		return 1
	}
	printExecutionPlanResponse(stdout, response)
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent work [options] <goal>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintf(w, "  --socket string\n    \tagentd Unix socket path (default %q)\n", cli.DefaultSocketPath)
	fmt.Fprintln(w, "  --timeout duration")
	fmt.Fprintln(w, "    \trequest timeout (default 10s)")
	fmt.Fprintln(w, "  --cwd string")
	fmt.Fprintln(w, "    \tapplication working directory")
	fmt.Fprintln(w, "  --session string")
	fmt.Fprintln(w, "    \texecution session/task id override")
	fmt.Fprintln(w, "  --dry-run")
	fmt.Fprintln(w, "    \tprint the /v1/requests envelope without submitting it")
	fmt.Fprintln(w, "  --auto-test")
	fmt.Fprintln(w, "    \trun go test ./... in the test pane")
}

func resolveCWD(value string) (string, error) {
	cwd := strings.TrimSpace(value)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
		info, err := os.Stat(cwd)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s is not a directory", cwd)
		}
		cwd = publicVarAlias(cwd, info)
		return cwd, nil
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

func publicVarAlias(path string, info os.FileInfo) string {
	const privateVarPrefix = "/private/var/"
	if !strings.HasPrefix(path, privateVarPrefix) {
		return path
	}
	candidate := "/var/" + strings.TrimPrefix(path, privateVarPrefix)
	candidateInfo, err := os.Stat(candidate)
	if err != nil {
		return path
	}
	if os.SameFile(info, candidateInfo) {
		return candidate
	}
	return path
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

func writeEnvelope(w io.Writer, envelope transport.RequestEnvelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

func printExecutionPlanResponse(w io.Writer, response transport.ExecutionPlanResponse) {
	totalPanes := 0
	for _, tab := range response.Tabs {
		totalPanes += len(tab.Panes)
	}
	fmt.Fprintf(w, "request=%s session=%s layout=%s tabs=%d panes=%d\n", response.RequestID, response.Session, response.Layout, len(response.Tabs), totalPanes)
	for _, tab := range response.Tabs {
		fmt.Fprintf(w, "tab=%s panes=%d\n", tab.Name, len(tab.Panes))
		for _, pane := range tab.Panes {
			fmt.Fprintf(w, "- %s role=%s status=%s", pane.ID, pane.Role, pane.Status)
			if pane.ZellijPaneID != "" {
				fmt.Fprintf(w, " zellij_pane_id=%s", pane.ZellijPaneID)
			}
			if pane.AgentID != "" {
				fmt.Fprintf(w, " agent_id=%s", pane.AgentID)
			}
			fmt.Fprintln(w)
		}
	}
}
