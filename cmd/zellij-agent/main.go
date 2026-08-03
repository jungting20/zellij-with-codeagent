package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	agentcli "zellij-with-codeagent/internal/cli/agent"
	chromecli "zellij-with-codeagent/internal/cli/chrome"
	codereviewcli "zellij-with-codeagent/internal/cli/codereview"
	ctlcli "zellij-with-codeagent/internal/cli/ctl"
	daemoncli "zellij-with-codeagent/internal/cli/daemon"
	dashboardcli "zellij-with-codeagent/internal/cli/dashboard"
	debatebg "zellij-with-codeagent/internal/cli/debatebackground"
	plannercli "zellij-with-codeagent/internal/cli/planner"
	rolecli "zellij-with-codeagent/internal/cli/role"
	ticketworkercli "zellij-with-codeagent/internal/cli/ticketworker"
	workcli "zellij-with-codeagent/internal/cli/work"
	"zellij-with-codeagent/internal/dashboard"
	"zellij-with-codeagent/internal/transport"
)

var getWorkingDirectory = os.Getwd

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "daemon":
		return daemoncli.Run(args[1:], stdout, stderr)
	case "ctl":
		return ctlcli.Run(args[1:], stdin, stdout, stderr, newClient)
	case "planner":
		return plannercli.RunWithInputConfig(args[1:], stdin, stdout, stderr, newPlannerClient, plannercli.Config{
			DefaultRoleCommand: []string{executablePath(), "role"},
		})
	case "work":
		return workcli.Run(args[1:], stdin, stdout, stderr, newWorkClient, workcli.Config{
			DefaultRoleCommand: []string{executablePath(), "role"},
		})
	case "chrome":
		return chromecli.Run(args[1:], stdin, stdout, stderr, newChromeClient, chromecli.Config{
			DefaultRoleCommand: []string{executablePath(), "role"},
		})
	case "dashboard":
		return dashboardcli.Run(args[1:], stdin, stdout, stderr, newDashboardClient, dashboardcli.Config{})
	case "agent":
		return agentcli.Run(args[1:], stdin, stdout, stderr, newAgentClient, agentcli.Config{
			Getwd:  os.Getwd,
			Getenv: os.Getenv,
		})
	case "ticket-worker":
		cwd, err := getWorkingDirectory()
		if err != nil {
			fmt.Fprintf(stderr, "determine working directory: %v\n", err)
			return ticketworkercli.ExitDatabase
		}
		return ticketworkercli.Run(context.Background(), args[1:], stdout, stderr, ticketworkercli.Dependencies{
			StartDirectory: cwd,
			Now:            time.Now,
			Executable:     []string{executablePath()},
			NewClient:      newTicketWorkerClient,
		})
	case "code-review":
		return codereviewcli.Run(args[1:], stdout, stderr)
	case "debate-background":
		return debatebg.Run(args[1:], stdout, stderr)
	case "role":
		return rolecli.Run(args[1:])
	default:
		fmt.Fprintf(stderr, "unknown command group: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func newClient(socketPath string, timeout time.Duration) ctlcli.AgentClient {
	return newAutoStartClient(socketPath, timeout)
}

func newPlannerClient(socketPath string, timeout time.Duration) plannercli.AgentClient {
	return newAutoStartClient(socketPath, timeout)
}

func newWorkClient(socketPath string, timeout time.Duration) workcli.AgentClient {
	return newAutoStartClient(socketPath, timeout)
}

func newChromeClient(socketPath string, timeout time.Duration) chromecli.AgentClient {
	return newAutoStartClient(socketPath, timeout)
}

var newTicketWorkerClient ticketworkercli.ClientFactory = func(socketPath string, timeout time.Duration) ticketworkercli.AgentClient {
	return newAutoStartClient(socketPath, timeout)
}

var newAgentClient agentcli.ClientFactory = func(socketPath string, timeout time.Duration) agentcli.AgentClient {
	return newAutoStartClient(socketPath, timeout)
}

func newDashboardClient(socketPath string, timeout time.Duration) dashboard.Client {
	return newAutoStartClient(socketPath, timeout)
}

func newAutoStartClient(socketPath string, timeout time.Duration) *transport.Client {
	return transport.NewClient(transport.ClientOptions{
		SocketPath: socketPath,
		Timeout:    timeout,
		AutoStart:  true,
		DaemonCommand: []string{
			executablePath(),
			"daemon",
			"serve",
			"--socket",
			socketPath,
		},
	})
}

func executablePath() string {
	path, err := os.Executable()
	if err == nil && path != "" {
		return path
	}
	return os.Args[0]
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  daemon   Run the local runtime daemon")
	fmt.Fprintln(w, "  ctl      Inspect and control the daemon runtime")
	fmt.Fprintln(w, "  planner  Generate, validate, and submit planner requests")
	fmt.Fprintln(w, "  work     Start a personal mixed-mode coding workspace")
	fmt.Fprintln(w, "  chrome   Start a Chrome network tracking tab")
	fmt.Fprintln(w, "  dashboard")
	fmt.Fprintln(w, "           Supervise the managed runtime in a live TUI")
	fmt.Fprintln(w, "  agent    Manage coding agents in the current Zellij pane through close-on-exit")
	fmt.Fprintln(w, "  ticket-worker")
	fmt.Fprintln(w, "           Manage a project-local SQLite ticket queue")
	fmt.Fprintln(w, "  code-review")
	fmt.Fprintln(w, "           Review the latest git diff with debate-background")
	fmt.Fprintln(w, "  debate-background")
	fmt.Fprintln(w, "           Run a daemonless multi-agent debate with stdout commands")
	fmt.Fprintln(w, "  role     Run a pane role process")
}
