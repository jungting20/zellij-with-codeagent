// Package agentticket adapts dashboard ticket actions to the public ticket-worker CLI.
package agentticket

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"zellij-with-codeagent/internal/ticketworker"
)

type Request struct {
	DefaultAgent                                      string
	Action, Directory, Agent, Prompt, Session, Socket string
	Timeout                                           time.Duration
}

func Command(ctx context.Context, req Request) (*exec.Cmd, error) {
	if strings.TrimSpace(req.Directory) == "" {
		return nil, fmt.Errorf("agent has no working directory")
	}
	args := []string{"ticket-worker", req.Action}
	switch req.Action {
	case "start":
		if strings.TrimSpace(req.Session) == "" {
			return nil, fmt.Errorf("agent has no Zellij session")
		}
		args = append(args, "--zellij-session", req.Session)
		if req.DefaultAgent != "" {
			args = append(args, "--default-agent", req.DefaultAgent)
		}
		if req.Socket != "" {
			args = append(args, "--socket", req.Socket)
		}
		if req.Timeout > 0 {
			args = append(args, "--timeout", req.Timeout.String())
		}
	case "add":
		prompt := strings.TrimSpace(req.Prompt)
		if prompt == "" {
			return nil, fmt.Errorf("ticket prompt is required")
		}
		title := strings.SplitN(prompt, "\n", 2)[0]
		if len([]rune(title)) > 80 {
			title = string([]rune(title)[:80])
		}
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		args = append(args, "--title", title, "--summary", title, "--worktree-branch", fmt.Sprintf("ticket/%x", nonce), "--prompt", req.Prompt, "--json")
		if req.Agent != "" {
			args = append(args, "--agent", req.Agent)
		}
	case "list":
		args = append(args, "--json")
	default:
		return nil, fmt.Errorf("unknown ticket action %q", req.Action)
	}
	cmd := exec.CommandContext(ctx, "zellij-agent", args...)
	cmd.Dir = req.Directory
	return cmd, nil
}

func Execute(ctx context.Context, req Request) (string, error) {
	cmd, err := Command(ctx, req)
	if err != nil {
		return "", err
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ticket %s failed: %w: %s", req.Action, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func Run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: agent-ticket <start|add|list> [options] <path>")
		return 2
	}
	req := Request{Action: args[0]}
	flags := flag.NewFlagSet("agent-ticket", flag.ContinueOnError)
	flags.StringVar(&req.DefaultAgent, "default-agent", "", "worker agent for start (default from project config)")
	flags.StringVar(&req.Prompt, "prompt", "", "ticket prompt")
	flags.StringVar(&req.Agent, "agent", "codex", "coding agent kind")
	flags.StringVar(&req.Session, "zellij-session", os.Getenv("ZELLIJ_SESSION_NAME"), "target Zellij session")
	flags.StringVar(&req.Socket, "socket", "/tmp/agentd.sock", "daemon socket")
	flags.DurationVar(&req.Timeout, "timeout", 15*time.Second, "request timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 1 || req.Timeout <= 0 {
		fmt.Fprintln(os.Stderr, "expected one project path and a positive timeout")
		return 2
	}
	req.Directory = flags.Arg(0)
	output, err := Execute(context.Background(), req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(os.Stdout, output)
	return 0
}

// DefaultAgent reads the selected project's configured worker agent without changing it.
func DefaultAgent(directory string) (string, error) {
	root, err := ticketworker.FindRoot(directory)
	if err != nil {
		return "", err
	}
	cfg, err := ticketworker.LoadConfig(root)
	if err != nil {
		return "", err
	}
	return cfg.DefaultAgent, nil
}
