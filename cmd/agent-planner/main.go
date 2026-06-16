package main

import (
	"io"
	"os"
	"time"

	plannercli "zellij-with-codeagent/internal/cli/planner"
	"zellij-with-codeagent/internal/transport"
)

type agentClient = plannercli.AgentClient
type clientFactory = plannercli.ClientFactory

func main() {
	os.Exit(runWithInput(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, func(socketPath string, timeout time.Duration) agentClient {
		return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
	}))
}

func run(args []string, stdout, stderr io.Writer, newClient clientFactory) int {
	return plannercli.Run(args, stdout, stderr, newClient)
}

func runWithInput(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient clientFactory) int {
	return plannercli.RunWithInput(args, stdin, stdout, stderr, newClient)
}

func defaultAgentRoleBin() string {
	return plannercli.DefaultAgentRoleBin()
}

func extractURL(text string) (string, bool) {
	return plannercli.ExtractURL(text)
}
