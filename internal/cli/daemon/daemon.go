package daemoncli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/registry"
	agentruntime "zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/transport"
	"zellij-with-codeagent/internal/zellij"
)

const version = "dev"

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

func Run(args []string, stdout, stderr io.Writer) int {
	return RunContext(context.Background(), args, stdout, stderr)
}

func RunContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
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
	case "serve":
		socketPath, ok := parseServeArgs(args[1:], stderr)
		if !ok {
			return 2
		}
		server, err := transport.NewServer(transport.ServerOptions{
			Service:    newRuntimeService(),
			SocketPath: socketPath,
			Version:    version,
		})
		if err != nil {
			fmt.Fprintf(stderr, "start transport server: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentd serving on unix socket %s\n", socketPath)
		if err := server.ListenAndServe(ctx); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			fmt.Fprintf(stderr, "agentd serve failed: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown argument: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agentd [--help] [--version] [serve [--socket <path>]]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "agentd is the daemon entrypoint for the Zellij agent runtime.")
}

func parseServeArgs(args []string, stderr io.Writer) (string, bool) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	if err := fs.Parse(args); err != nil {
		return "", false
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", fs.Args())
		printUsage(stderr)
		return "", false
	}
	return *socketPath, true
}

func newRuntimeService() *agentruntime.Service {
	return agentruntime.NewService(agentruntime.Options{
		Registry:           registry.New(),
		Backend:            zellij.NewBackend(zellij.Options{}),
		SubscriptionRunner: agentruntime.ExecSubscriptionRunner{},
	})
}
