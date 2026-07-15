package codereview

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	debatebg "zellij-with-codeagent/internal/cli/debatebackground"
)

const ReviewTopic = `Review the latest git diff.
Focus on correctness, edge cases, regressions, duplication, repeated business logic, and test gaps.
Do not rewrite the whole code.
Only leave actionable review comments.`

type BackgroundRun func([]string, io.Writer, io.Writer) int

func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, debatebg.Run)
}

func run(args []string, stdout, stderr io.Writer, backgroundRun BackgroundRun) int {
	fs := flag.NewFlagSet("code-review", flag.ContinueOnError)
	if isHelpRequest(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: zellij-agent code-review [options]")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Options:")
		fs.PrintDefaults()
	}
	rounds := fs.Int("rounds", 1, "number of review rounds to run, from 1 to 3")
	prompt := fs.String("prompt", "", "additional review prompt")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "zellij-agent code-review: unexpected argument: %s\n", fs.Arg(0))
		fs.Usage()
		return 2
	}

	return backgroundRun([]string{
		"--topic", reviewTopic(*prompt),
		"--rounds", strconv.Itoa(*rounds),
		"--start-codex",
	}, stdout, stderr)
}

func reviewTopic(prompt string) string {
	extra := strings.TrimSpace(prompt)
	if extra == "" {
		return ReviewTopic
	}
	return ReviewTopic + "\n\nAdditional review prompt:\n" + extra
}

func isHelpRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help")
}
