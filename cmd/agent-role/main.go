package main

import (
	"flag"
	"fmt"
	"os"

	"zellij-with-codeagent/cmd/agent-role/coder"
	"zellij-with-codeagent/cmd/agent-role/console"
	"zellij-with-codeagent/cmd/agent-role/network"
)

// Print usage helper
func printUsage() {
	fmt.Println("Usage: agent-role <role> [options]")
	fmt.Println("Available roles:")
	fmt.Println("  coder                   - Visualizes coding agent status")
	fmt.Println("  network-tracker --url   - Visualizes network tracking for a specific URL")
	fmt.Println("  console-tracker --url   - Visualizes console log tracking for a specific URL")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	role := os.Args[1]
	switch role {
	case "coder":
		coder.Run()
	case "network-tracker":
		runNetworkTracker(os.Args[2:])
	case "console-tracker":
		runConsoleTracker(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown role '%s'\n", role)
		printUsage()
		os.Exit(1)
	}
}

// runNetworkTracker handles network tracker command arguments
func runNetworkTracker(args []string) {
	fs := flag.NewFlagSet("network-tracker", flag.ExitOnError)
	urlPtr := fs.String("url", "", "Target URL to track network activity")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if urlPtr == nil || *urlPtr == "" {
		fmt.Fprintln(os.Stderr, "Error: --url parameter is required for network-tracker")
		fs.Usage()
		os.Exit(1)
	}

	network.Run(*urlPtr)
}

// runConsoleTracker handles console tracker command arguments
func runConsoleTracker(args []string) {
	fs := flag.NewFlagSet("console-tracker", flag.ExitOnError)
	urlPtr := fs.String("url", "", "Target URL to track console logs")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if urlPtr == nil || *urlPtr == "" {
		fmt.Fprintln(os.Stderr, "Error: --url parameter is required for console-tracker")
		fs.Usage()
		os.Exit(1)
	}

	console.Run(*urlPtr)
}
