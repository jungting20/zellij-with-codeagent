package debatebackground

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"zellij-with-codeagent/internal/backgrounddebate"
)

const defaultOutputPath = "/tmp"

type CodexStartRequest struct {
	Command    []string
	CWD        string
	PromptFile string
}

type CodexStarter interface {
	Start(context.Context, CodexStartRequest) error
}

type Dependencies struct {
	Runner       backgrounddebate.RoleRunner
	CodexStarter CodexStarter
	Now          func() time.Time
}

type processCodexStarter struct{}

func Run(args []string, stdout, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "zellij-agent debate-background executable failed: %v\n", err)
		return 1
	}
	runner, err := backgrounddebate.NewProcessRoleRunner([]string{executable})
	if err != nil {
		fmt.Fprintf(stderr, "zellij-agent debate-background runner failed: %v\n", err)
		return 1
	}
	return run(args, os.Stdin, stdout, stderr, Dependencies{
		Runner:       runner,
		CodexStarter: processCodexStarter{},
		Now:          time.Now,
	})
}

func run(args []string, _ io.Reader, stdout, stderr io.Writer, deps Dependencies) int {
	fs := flag.NewFlagSet("debate-background", flag.ContinueOnError)
	if isHelpRequest(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.Usage = func() { printUsage(fs.Output()) }

	timeout := fs.Duration("timeout", 10*time.Minute, "overall command timeout")
	topic := fs.String("topic", "", "debate topic")
	fs.String("agents", "agy,agent,codex", "deprecated compatibility option; accepted and ignored")
	rounds := fs.Int("rounds", 1, "number of debate rounds to run, from 1 to 3")
	agentTimeout := fs.Duration("agent-timeout", 2*time.Minute, "per-role response timeout")
	fs.String("config", "", "deprecated compatibility option; accepted and ignored")
	cwd := fs.String("cwd", ".", "repository path for role commands")
	outputPath := fs.String("output", defaultOutputPath, "file or directory for the saved debate result")
	outputFormat := fs.String("output-format", "text", "output format: text or json")
	startCodex := fs.Bool("start-codex", false, "start Codex after a successful text result")
	codexBin := fs.String("codex-bin", "codex", "Codex executable used with --start-codex")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "Error: unexpected positional arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	seen := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if seen["agents"] {
		fmt.Fprintln(stderr, "warning: --agents is deprecated and ignored; roles are fixed to debate-proposer, debate-critic, and debate-judge")
	}
	if seen["config"] {
		fmt.Fprintln(stderr, "warning: --config is deprecated and ignored; role commands use the fixed pipeline")
	}

	if strings.TrimSpace(*topic) == "" {
		fmt.Fprintln(stderr, "Error: --topic is required")
		return 2
	}
	if *rounds < 1 || *rounds > 3 {
		fmt.Fprintln(stderr, "Error: --rounds must be between 1 and 3")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "Error: --timeout must be greater than zero")
		return 2
	}
	if *agentTimeout <= 0 {
		fmt.Fprintln(stderr, "Error: --agent-timeout must be greater than zero")
		return 2
	}
	if *outputFormat != "text" && *outputFormat != "json" {
		fmt.Fprintln(stderr, "Error: --output-format must be text or json")
		return 2
	}
	if *outputFormat == "json" && *startCodex {
		fmt.Fprintln(stderr, "Error: --start-codex requires --output-format text")
		return 2
	}
	repository, err := resolveRepository(*cwd)
	if err != nil {
		fmt.Fprintf(stderr, "Error: --cwd must resolve to an accessible git repository: %v\n", err)
		return 2
	}
	if deps.Runner == nil {
		fmt.Fprintln(stderr, "zellij-agent debate-background runner is unavailable")
		return 1
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result := backgrounddebate.Run(ctx, deps.Runner, backgrounddebate.Options{
		Topic:        strings.TrimSpace(*topic),
		Repository:   repository,
		Rounds:       *rounds,
		AgentTimeout: *agentTimeout,
		Progress:     progressWriter(stderr),
	})

	resolvedOutput, err := resolveOutputPath(*outputPath, *outputFormat, deps.Now())
	if err != nil {
		return printPersistenceFailure(stdout, stderr, result, *outputFormat, err)
	}
	result.OutputPath = resolvedOutput
	rendered, err := renderResult(result, *outputFormat)
	if err != nil {
		return printPersistenceFailure(stdout, stderr, result, *outputFormat, err)
	}
	if err := writeAtomic(resolvedOutput, rendered); err != nil {
		return printPersistenceFailure(stdout, stderr, result, *outputFormat, err)
	}

	if *outputFormat == "json" {
		fmt.Fprintf(stderr, "saved debate output to %s\n", resolvedOutput)
		_, _ = stdout.Write(rendered)
	} else {
		fmt.Fprintf(stdout, "saved debate output to %s\n", resolvedOutput)
		_, _ = stdout.Write(rendered)
	}
	if result.Status != backgrounddebate.StatusSuccess {
		return 1
	}
	if *startCodex {
		if deps.CodexStarter == nil {
			fmt.Fprintln(stderr, "zellij-agent debate-background codex starter is unavailable")
			return 1
		}
		req := CodexStartRequest{
			Command:    codexCommand(*codexBin, repository, resolvedOutput),
			CWD:        repository,
			PromptFile: resolvedOutput,
		}
		if err := deps.CodexStarter.Start(ctx, req); err != nil {
			fmt.Fprintf(stderr, "zellij-agent debate-background codex start failed: %v\n", err)
			return 1
		}
	}
	return 0
}

func isHelpRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help")
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent debate-background [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Runs the fixed debate-proposer, debate-critic, and debate-judge role pipeline.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  --topic string                 Debate topic (required).")
	fmt.Fprintln(w, "  --rounds int                   Number of rounds, from 1 to 3 (default 1).")
	fmt.Fprintln(w, "  --cwd path                     Accessible Git repository (default .).")
	fmt.Fprintln(w, "  --timeout duration             Overall command timeout (default 10m).")
	fmt.Fprintln(w, "  --agent-timeout duration       Per-role timeout (default 2m).")
	fmt.Fprintln(w, "  --output path                  Result file or directory (default /tmp).")
	fmt.Fprintln(w, "  --output-format text|json      Output format (default text).")
	fmt.Fprintln(w, "  --start-codex                  Start Codex after success; --start-codex is available only with text output.")
	fmt.Fprintln(w, "  --codex-bin path               Codex executable (default codex).")
	fmt.Fprintln(w, "  --agents string                deprecated; accepted and ignored. The role pipeline is fixed.")
	fmt.Fprintln(w, "  --config path                  deprecated; accepted and ignored. The role pipeline is fixed.")
}

func progressWriter(stderr io.Writer) func(backgrounddebate.ProgressEvent) {
	return func(event backgrounddebate.ProgressEvent) {
		if event.Role == backgrounddebate.Proposer.Name && event.Status == "started" {
			fmt.Fprintf(stderr, "[debate progress] round=%d/%d status=started\n", event.Round, event.Rounds)
		}
		fmt.Fprintf(stderr, "[debate progress] round=%d/%d role=%s status=%s\n", event.Round, event.Rounds, event.Role, event.Status)
		if event.Role == backgrounddebate.Judge.Name && event.Status == "completed" {
			fmt.Fprintf(stderr, "[debate progress] round=%d/%d status=completed\n", event.Round, event.Rounds)
		}
	}
}

func resolveRepository(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("access %q: %w", absPath, err)
	}
	searchPath := absPath
	if !info.IsDir() {
		searchPath = filepath.Dir(searchPath)
	}
	for {
		if _, err := os.Stat(filepath.Join(searchPath, ".git")); err == nil {
			return searchPath, nil
		}
		parent := filepath.Dir(searchPath)
		if parent == searchPath {
			break
		}
		searchPath = parent
	}
	return "", fmt.Errorf("path %q is not inside a git repository", absPath)
}

func codexCommand(codexBin, cwd, outputPath string) []string {
	bin := strings.TrimSpace(codexBin)
	if bin == "" {
		bin = "codex"
	}
	return []string{
		bin,
		"--cd", cwd,
		"--add-dir", filepath.Dir(outputPath),
		fmt.Sprintf("The completed debate is saved at %s. Read it and continue from the final judge recommendation.", outputPath),
	}
}

func (processCodexStarter) Start(ctx context.Context, req CodexStartRequest) error {
	if len(req.Command) == 0 {
		return errors.New("codex command is required")
	}
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	cmd.Dir = req.CWD
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func printPersistenceFailure(stdout, stderr io.Writer, result backgrounddebate.Result, outputFormat string, err error) int {
	fmt.Fprintf(stderr, "zellij-agent debate-background output file failed: %v\n", err)
	result.Status = backgrounddebate.StatusFailed
	result.Failure = &backgrounddebate.Failure{Kind: backgrounddebate.FailurePersistence, Message: err.Error()}
	rendered, renderErr := renderResult(result, outputFormat)
	if renderErr != nil {
		fmt.Fprintf(stderr, "zellij-agent debate-background render failed: %v\n", renderErr)
		return 1
	}
	_, _ = stdout.Write(rendered)
	return 1
}
