package debatebackground

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"zellij-with-codeagent/internal/debate"
)

type CodexStartRequest struct {
	Command       []string
	CWD           string
	InitialPrompt string
}

type CodexStarter interface {
	Start(context.Context, CodexStartRequest) error
}

type codexStarterFunc func(context.Context, CodexStartRequest) error

func (fn codexStarterFunc) Start(ctx context.Context, req CodexStartRequest) error {
	return fn(ctx, req)
}

var defaultCodexStarter CodexStarter = codexStarterFunc(startCodex)

func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("debate-background", flag.ContinueOnError)
	if isHelpRequest(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: zellij-agent debate-background [options]")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Options:")
		fs.PrintDefaults()
	}
	timeout := fs.Duration("timeout", 10*time.Minute, "overall command timeout")
	topic := fs.String("topic", "", "debate topic")
	agentsCSV := fs.String("agents", "agy,agent,codex", "comma-separated agent ids")
	rounds := fs.Int("rounds", 1, "number of debate rounds to run, from 1 to 3")
	agentTimeout := fs.Duration("agent-timeout", 2*time.Minute, "per-agent debate response timeout")
	configPath := fs.String("config", "", "YAML file defining debate background agent commands")
	cwd := fs.String("cwd", ".", "working directory for agent commands")
	startCodex := fs.Bool("start-codex", false, "start Codex after the debate using the printed result as the initial prompt")
	codexBin := fs.String("codex-bin", "codex", "Codex executable used with --start-codex")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	result, err := debate.RunBackground(ctx, debate.BackgroundOptions{
		Topic:        *topic,
		Agents:       debate.ParseAgents(*agentsCSV),
		Rounds:       *rounds,
		AgentTimeout: *agentTimeout,
		ConfigPath:   *configPath,
		CWD:          *cwd,
		Progress:     stdout,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		if debate.IsValidationError(err) {
			return 2
		}
		return 1
	}

	var resultOutput strings.Builder
	debate.PrintResult(&resultOutput, result)
	printedResult := resultOutput.String()
	fmt.Fprint(stdout, printedResult)
	if *startCodex {
		fmt.Fprintln(stdout, "\n[debate-background codex]")
		fmt.Fprintln(stdout, "starting Codex with debate output as initial prompt...")
		req := CodexStartRequest{
			Command:       codexCommand(*codexBin, *cwd, printedResult),
			CWD:           *cwd,
			InitialPrompt: printedResult,
		}
		if err := defaultCodexStarter.Start(context.Background(), req); err != nil {
			fmt.Fprintf(stderr, "zellij-agent debate-background codex start failed: %v\n", err)
			return 1
		}
	}
	return 0
}

func isHelpRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help")
}

func SetBackgroundRunnerForTesting(runner debate.BackgroundCommandRunner) func() {
	return debate.SetBackgroundRunnerForTesting(runner)
}

func SetCodexStarterForTesting(starter CodexStarter) func() {
	previous := defaultCodexStarter
	defaultCodexStarter = starter
	return func() {
		defaultCodexStarter = previous
	}
}

func codexCommand(codexBin, cwd, initialPrompt string) []string {
	bin := strings.TrimSpace(codexBin)
	if bin == "" {
		bin = "codex"
	}
	command := []string{bin}
	if strings.TrimSpace(cwd) != "" {
		command = append(command, "--cd", cwd)
	}
	return append(command, initialPrompt)
}

func startCodex(ctx context.Context, req CodexStartRequest) error {
	if len(req.Command) == 0 {
		return errors.New("codex command is required")
	}
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	if strings.TrimSpace(req.CWD) != "" {
		cmd.Dir = req.CWD
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
