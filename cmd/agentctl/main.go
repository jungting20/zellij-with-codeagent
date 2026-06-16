package main

import (
	"io"
	"os"
	"time"

	ctlcli "zellij-with-codeagent/internal/cli/ctl"
	"zellij-with-codeagent/internal/transport"
)

type agentClient = ctlcli.AgentClient
type clientFactory = ctlcli.ClientFactory

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, func(socketPath string, timeout time.Duration) agentClient {
		return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
	}))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient clientFactory) int {
	return ctlcli.Run(args, stdin, stdout, stderr, newClient)
}
