package rolecli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"zellij-with-codeagent/cmd/agent-role/coder"
	"zellij-with-codeagent/cmd/agent-role/console"
	"zellij-with-codeagent/cmd/agent-role/editor"
	"zellij-with-codeagent/cmd/agent-role/lsp"
	"zellij-with-codeagent/cmd/agent-role/network"
	"zellij-with-codeagent/internal/roles"
)

// Print usage helper
func printUsage() {
	fmt.Println("Usage: agent-role <role> [options]")
	fmt.Println("Available roles:")
	for _, role := range roles.All() {
		fmt.Printf("  %-28s - %s\n", role.Usage, role.Description)
	}
	fmt.Println("  roles [--json]              - Lists available role descriptions")
}

func main() {
	os.Exit(Run(os.Args[1:]))
}

func Run(args []string) int {
	if len(args) < 1 {
		printUsage()
		return 1
	}

	role := args[0]
	switch role {
	case roles.RoleCoder:
		coder.Run()
	case roles.RoleEditor:
		editor.Run(args[1:])
	case roles.RoleLSP:
		lsp.Run(args[1:])
	case roles.RoleNetworkTracker:
		runNetworkTracker(args[1:])
	case roles.RoleConsoleTracker:
		runConsoleTracker(args[1:])
	case "roles":
		runRoles(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown role '%s'\n", role)
		printUsage()
		return 1
	}
	return 0
}

func runRoles(args []string) {
	fs := flag.NewFlagSet("roles", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "print role descriptions as JSON")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fs.Usage()
		os.Exit(2)
	}

	roleSpecs := roles.All()
	if *jsonOutput {
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(roleSpecs); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding roles: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(buf.String())
		return
	}

	for _, role := range roleSpecs {
		fmt.Printf("%-28s %s\n", role.Usage, role.Description)
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
