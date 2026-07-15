package debateproposer

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"zellij-with-codeagent/internal/debaterole"
)

//go:embed system_prompt.txt
var systemPrompt string

const maxContentChars = 2000

type agyProvider struct{}

func (agyProvider) Run(ctx context.Context, req debaterole.ProviderRequest) (string, error) {
	path, err := exec.LookPath("agy")
	if err != nil {
		return "", fmt.Errorf("agy executable not found on PATH")
	}
	cmd := exec.CommandContext(ctx, path, "--new-project", "--mode", "plan", "--print", req.Prompt)
	cmd.Dir = req.Repository
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("agy failed: %w%s", err, diagnostic(stderr.String()))
	}
	return stdout.String(), nil
}

func Run(args []string) int {
	return runWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return debaterole.Run(args, stdin, stdout, stderr, roleConfig(agyProvider{}))
}

func roleConfig(provider debaterole.Provider) debaterole.Config {
	return debaterole.Config{
		Role:            "debate-proposer",
		Engine:          "agy",
		SystemPrompt:    systemPrompt,
		Provider:        provider,
		MaxContentChars: maxContentChars,
	}
}

func diagnostic(stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return ""
	}
	return ": " + strings.TrimSpace(stderr)
}
