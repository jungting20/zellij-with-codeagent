package rolecli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"zellij-with-codeagent/cmd/agent-role/agentdashboard"
	"zellij-with-codeagent/cmd/agent-role/agentnext"
	"zellij-with-codeagent/cmd/agent-role/agentprev"
	"zellij-with-codeagent/cmd/agent-role/coder"
	"zellij-with-codeagent/cmd/agent-role/codingagent"
	"zellij-with-codeagent/cmd/agent-role/console"
	"zellij-with-codeagent/cmd/agent-role/debatecoordinator"
	"zellij-with-codeagent/cmd/agent-role/debatecritic"
	"zellij-with-codeagent/cmd/agent-role/debatejudge"
	"zellij-with-codeagent/cmd/agent-role/debateproposer"
	"zellij-with-codeagent/cmd/agent-role/editor"
	"zellij-with-codeagent/cmd/agent-role/loopprojectagent"
	"zellij-with-codeagent/cmd/agent-role/lsp"
	"zellij-with-codeagent/cmd/agent-role/network"
	"zellij-with-codeagent/cmd/agent-role/tabnetwork"
	"zellij-with-codeagent/cmd/agent-role/tabwatcher"
	"zellij-with-codeagent/cmd/agent-role/ticketmanager"
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
		return network.Run(args[1:])
	case roles.RoleConsoleTracker:
		return console.Run(args[1:])
	case roles.RoleTabNetwork:
		return tabnetwork.Run(args[1:])
	case roles.RoleTabWatcher:
		return tabwatcher.Run(args[1:])
	case roles.RoleCodingAgent:
		return codingagent.Run(args[1:])
	case roles.RoleAgentNext:
		return agentnext.Run(args[1:])
	case roles.RoleAgentPrev:
		return agentprev.Run(args[1:])
	case roles.RoleAgentDashboard:
		return agentdashboard.Run(args[1:])
	case roles.RoleLoopProjectWorker:
		return loopprojectagent.RunWorker(args[1:])
	case roles.RoleLoopProjectVerifier:
		return loopprojectagent.RunVerifier(args[1:])
	case roles.RoleTicketManager:
		return ticketmanager.Run(args[1:])
	case roles.RoleDebateCoordinator:
		return debatecoordinator.Run(args[1:])
	case roles.RoleDebateProposer:
		return debateproposer.Run(args[1:])
	case roles.RoleDebateCritic:
		return debatecritic.Run(args[1:])
	case roles.RoleDebateJudge:
		return debatejudge.Run(args[1:])
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
