package loopprojectagent

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

type Mode string

const (
	ModeWorker   Mode = "worker"
	ModeVerifier Mode = "verifier"
)

func RunWorker(args []string) int {
	return run(ModeWorker, args)
}

func RunVerifier(args []string) int {
	return run(ModeVerifier, args)
}

func run(mode Mode, args []string) int {
	cmd, err := prepare(mode, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "Error running loop project %s: %v\n", mode, err)
		return 1
	}
	return 0
}

func prepare(mode Mode, args []string) (*exec.Cmd, error) {
	fs := flag.NewFlagSet("loop-project-"+string(mode), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repository := fs.String("repository", "", "repository path")
	runnerSkill := fs.String("runner-skill", "", "runner skill path")
	orchestratorPane := fs.String("orchestrator-pane", "", "orchestrator logical pane ID")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *repository == "" || *runnerSkill == "" || *orchestratorPane == "" {
		return nil, fmt.Errorf("usage: loop-project-%s --repository PATH --runner-skill PATH --orchestrator-pane PANE_ID", mode)
	}

	repositoryPath, err := absoluteDirectoryWithFile(*repository, ".git")
	if err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}
	runnerSkillPath, err := absoluteDirectoryWithFile(*runnerSkill, "SKILL.md")
	if err != nil {
		return nil, fmt.Errorf("runner skill: %w", err)
	}

	zellijAgentPath, err := exec.LookPath("zellij-agent")
	if err != nil {
		return nil, fmt.Errorf("zellij-agent executable not found on PATH")
	}

	access := "full"
	if mode == ModeVerifier {
		access = "read-only"
	} else if mode != ModeWorker {
		return nil, fmt.Errorf("unsupported loop project mode %q", mode)
	}
	prompt := bootstrapPrompt(mode, repositoryPath, runnerSkillPath, *orchestratorPane)
	cmd := exec.Command("zellij-agent", "agent", "start", "codex", "--cwd", repositoryPath, "--access", access, "--", prompt)
	cmd.Path = zellijAgentPath
	cmd.Dir = repositoryPath
	return cmd, nil
}

func absoluteDirectoryWithFile(path, requiredFile string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", fmt.Errorf("path %q is not accessible: %w", absolutePath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", absolutePath)
	}
	if _, err := os.Stat(filepath.Join(absolutePath, requiredFile)); err != nil {
		return "", fmt.Errorf("path %q does not contain %s", absolutePath, requiredFile)
	}
	return absolutePath, nil
}

func bootstrapPrompt(mode Mode, repository, runnerSkill, orchestratorPane string) string {
	role := "loop-project-worker"
	permission := "You may make code changes only after receiving an assignment from the orchestrator."
	if mode == ModeVerifier {
		return fmt.Sprintf(`You are the loop-project-verifier.
Repository: %s
Runner skill: %s
Orchestrator logical pane ID: %s

Until the orchestrator sends an assignment, do not write or modify repository files.
code_changes: FORBIDDEN
do not access the daemon socket and do not send outbound zellij-agent control messages.
After read-only inspection, print exactly one result block to stdout and no second block:
LOOP_VERIFY_RESULT_BEGIN
protocol_version: 1
project_id: <assigned project id>
milestone_id: <assigned milestone id>
run_id: <assigned run id>
verdict: APPROVE | REJECT | UNCERTAIN
next_action: <one bounded action>
LOOP_VERIFY_RESULT_END`, repository, runnerSkill, orchestratorPane)
	}
	return fmt.Sprintf(`You are the %s.
Repository: %s
Runner skill: %s
Orchestrator logical pane ID: %s

Until the orchestrator sends an assignment, do not write or modify repository files.
%s
Use zellij-agent ctl message to report status and results to the orchestrator logical pane ID %s.`, role, repository, runnerSkill, orchestratorPane, permission, orchestratorPane)
}
