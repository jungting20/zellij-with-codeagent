package main

import (
	"context"
	"io"
	"os"

	daemoncli "zellij-with-codeagent/internal/cli/daemon"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return daemoncli.Run(args, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return daemoncli.RunContext(ctx, args, stdout, stderr)
}
