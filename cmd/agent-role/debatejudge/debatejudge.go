package debatejudge

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

type codexProvider struct{}

func (codexProvider) Run(ctx context.Context, req debaterole.ProviderRequest) (string, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("codex executable not found on PATH")
	}
	cmd := exec.CommandContext(ctx, path,
		"exec", "--sandbox", "read-only", "--cd", req.Repository, "-",
	)
	cmd.Dir = req.Repository
	cmd.Stdin = strings.NewReader(req.Prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("codex failed: %w%s", err, diagnostic(stderr.String()))
	}
	return stdout.String(), nil
}

func Run(args []string) int {
	return runWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return debaterole.Run(args, stdin, stdout, stderr, debaterole.Config{
		Role: "debate-judge", Engine: "codex", SystemPrompt: systemPrompt, Provider: codexProvider{},
	})
}

func diagnostic(stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return ""
	}
	return ": " + strings.TrimSpace(stderr)
}
