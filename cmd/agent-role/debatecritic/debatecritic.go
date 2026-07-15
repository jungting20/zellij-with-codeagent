package debatecritic

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
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

type cursorResult struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

type agentProvider struct{}

func (agentProvider) Run(ctx context.Context, req debaterole.ProviderRequest) (string, error) {
	path, err := exec.LookPath("agent")
	if err != nil {
		return "", fmt.Errorf("agent executable not found on PATH")
	}
	cmd := exec.CommandContext(ctx, path, "--print", "--mode", "ask", "--output-format", "json", "--trust", "--workspace", req.Repository, req.Prompt)
	cmd.Dir = req.Repository
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("agent failed: %w%s", err, diagnostic(stderr.String()))
	}

	var result cursorResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return "", fmt.Errorf("decode agent JSON result: %w%s", err, diagnostic(stderr.String()))
	}
	if result.Type != "result" || result.Subtype != "success" || result.IsError || strings.TrimSpace(result.Result) == "" {
		return "", fmt.Errorf("invalid agent JSON result: type=%q subtype=%q is_error=%t result_empty=%t%s",
			result.Type, result.Subtype, result.IsError, strings.TrimSpace(result.Result) == "", diagnostic(stderr.String()))
	}
	return result.Result, nil
}

func Run(args []string) int {
	return runWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return debaterole.Run(args, stdin, stdout, stderr, roleConfig(agentProvider{}))
}

func roleConfig(provider debaterole.Provider) debaterole.Config {
	return debaterole.Config{
		Role:            "debate-critic",
		Engine:          "agent",
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
