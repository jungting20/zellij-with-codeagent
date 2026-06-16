package editor

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

// Run opens the requested file in Neovim.
func Run(args []string) {
	fs := flag.NewFlagSet("editor", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: agent-role editor <file>\n")
	}
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(2)
	}

	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	cmd := exec.Command("nvim", fs.Arg(0))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error opening Neovim: %v\n", err)
		os.Exit(1)
	}
}
