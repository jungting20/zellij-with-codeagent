package ticketworkercli

import (
	"fmt"
	"io"
)

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		printUsage(stdout)
		return 0
	}
	fmt.Fprintln(stderr, "ticket-worker is not implemented")
	return 2
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent ticket-worker")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "ticket-worker is not implemented")
}
