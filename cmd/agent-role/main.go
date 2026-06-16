package main

import (
	"os"

	rolecli "zellij-with-codeagent/internal/cli/role"
)

func main() {
	os.Exit(rolecli.Run(os.Args[1:]))
}
