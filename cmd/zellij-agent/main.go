package main

import (
	"fmt"
	"io"
	"os"
	"time"

	ctlcli "zellij-with-codeagent/internal/cli/ctl"
	daemoncli "zellij-with-codeagent/internal/cli/daemon"
	plannercli "zellij-with-codeagent/internal/cli/planner"
	rolecli "zellij-with-codeagent/internal/cli/role"
	workcli "zellij-with-codeagent/internal/cli/work"
	"zellij-with-codeagent/internal/transport"
)

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
	case "role":
		return rolecli.Run(args[1:])
	default:
		fmt.Fprintf(stderr, "unknown command group: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func newClient(socketPath string, timeout time.Duration) ctlcli.AgentClient {
	return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
}

func newPlannerClient(socketPath string, timeout time.Duration) plannercli.AgentClient {
	return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
}

func newWorkClient(socketPath string, timeout time.Duration) workcli.AgentClient {
	return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
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
	fmt.Fprintln(w, "  role     Run a pane role process")
}
