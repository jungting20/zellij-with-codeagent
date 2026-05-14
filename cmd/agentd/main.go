package main

import (
	"fmt"
	"io"
	"os"

	"zellij-with-codeagent/internal/registry"
	agentruntime "zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/zellij"
)

const version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_ = newRuntimeService()
		fmt.Fprintln(stdout, "agentd daemon skeleton")
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "agentd %s\n", version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown argument: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agentd [--help] [--version]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "agentd is the daemon entrypoint for the Zellij agent runtime.")
}

func newRuntimeService() agentruntime.RuntimeService {
	return agentruntime.NewService(agentruntime.Options{
		Registry:           registry.New(),
		Backend:            zellij.NewBackend(zellij.Options{}),
		SubscriptionRunner: agentruntime.ExecSubscriptionRunner{},
	})
}
