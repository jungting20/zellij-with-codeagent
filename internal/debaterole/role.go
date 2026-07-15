package debaterole

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const SchemaVersion = "debate-role/v1"

type ProviderRequest struct {
	Repository string
	Prompt     string
}

type Provider interface {
	Run(context.Context, ProviderRequest) (string, error)
}

type ProviderFunc func(context.Context, ProviderRequest) (string, error)

func (fn ProviderFunc) Run(ctx context.Context, req ProviderRequest) (string, error) {
	return fn(ctx, req)
}

type Config struct {
	Role            string
	Engine          string
	SystemPrompt    string
	Provider        Provider
	MaxContentChars int
}

type Result struct {
	SchemaVersion string `json:"schema_version"`
	Role          string `json:"role"`
	Engine        string `json:"engine"`
	Status        string `json:"status"`
	Content       string `json:"content"`
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg Config) int {
	fs := flag.NewFlagSet(cfg.Role, flag.ContinueOnError)
	fs.SetOutput(stderr)
	outputFormat := fs.String("output-format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *outputFormat != "text" && *outputFormat != "json" {
		fmt.Fprintf(stderr, "Error: --output-format must be text or json\n")
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "Error: path is required")
		return 2
	}

	repository, err := resolveRepositoryPath(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	var input string
	if fs.NArg() > 1 {
		input = strings.Join(fs.Args()[1:], " ")
	} else {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "Error: read stdin: %v\n", err)
			return 1
		}
		input = string(data)
	}
	if strings.TrimSpace(input) == "" {
		fmt.Fprintln(stderr, "Error: prompt is required")
		return 2
	}
	if cfg.Provider == nil {
		fmt.Fprintln(stderr, "Error: provider is required")
		return 1
	}

	content, err := cfg.Provider.Run(context.Background(), ProviderRequest{
		Repository: repository,
		Prompt:     ComposePrompt(cfg.SystemPrompt, repositoryInput(repository, input)),
	})
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return 1
	}
	content = compactContent(content, cfg.MaxContentChars)
	if strings.TrimSpace(content) == "" {
		fmt.Fprintln(stderr, "Error: provider returned an empty response")
		return 1
	}

	if *outputFormat == "json" {
		err = json.NewEncoder(stdout).Encode(Result{
			SchemaVersion: SchemaVersion,
			Role:          cfg.Role,
			Engine:        cfg.Engine,
			Status:        "success",
			Content:       content,
		})
	} else {
		_, err = fmt.Fprintln(stdout, content)
	}
	if err != nil {
		fmt.Fprintf(stderr, "Error: write output: %v\n", err)
		return 1
	}
	return 0
}

const contentOmissionMarker = "\n\n[출력 길이 제한으로 중간 내용 생략]\n\n"

func compactContent(content string, maxChars int) string {
	if maxChars <= 0 {
		return strings.TrimRight(content, "\r\n")
	}
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	marker := []rune(contentOmissionMarker)
	remaining := maxChars - len(marker)
	if remaining <= 0 {
		return string(marker[:maxChars])
	}
	headCount := remaining * 70 / 100
	tailCount := remaining - headCount
	result := strings.TrimSpace(string(runes[:headCount])) + contentOmissionMarker +
		strings.TrimSpace(string(runes[len(runes)-tailCount:]))
	if resultRunes := []rune(result); len(resultRunes) > maxChars {
		result = string(resultRunes[:maxChars])
	}
	return result
}

func ComposePrompt(systemPrompt, input string) string {
	return "<<<SYSTEM_ROLE_BEGIN>>>\n" + strings.TrimSpace(systemPrompt) +
		"\n<<<SYSTEM_ROLE_END>>>\n\n<<<DEBATE_INPUT_BEGIN>>>\n" + strings.TrimSpace(input) +
		"\n<<<DEBATE_INPUT_END>>>\n"
}

func repositoryInput(repository, input string) string {
	return "<<<TARGET_REPOSITORY_BEGIN>>>\n" + strings.TrimSpace(repository) +
		"\n<<<TARGET_REPOSITORY_END>>>\n\n" +
		"Analyze only the target repository above. Do not reuse files or context from another project.\n\n" +
		"<<<USER_INPUT_BEGIN>>>\n" + strings.TrimSpace(input) + "\n<<<USER_INPUT_END>>>"
}

func resolveRepositoryPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("path %q is not accessible: %w", absPath, err)
	}
	searchPath := absPath
	if !info.IsDir() {
		searchPath = filepath.Dir(absPath)
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
