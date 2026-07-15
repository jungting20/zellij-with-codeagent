package debaterole

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRunResolvesInputAndRendersOutput(t *testing.T) {
	repo := newTestRepository(t)
	wantPrompt := func(input string) string {
		return "<<<SYSTEM_ROLE_BEGIN>>>\nSYSTEM\n<<<SYSTEM_ROLE_END>>>\n\n" +
			"<<<DEBATE_INPUT_BEGIN>>>\n" + repositoryInputForTest(repo, input) + "\n<<<DEBATE_INPUT_END>>>\n"
	}

	tests := []struct {
		name       string
		args       []string
		stdin      string
		format     string
		wantInput  string
		wantOutput string
	}{
		{
			name:       "positional text",
			args:       []string{repo, "analyze", "this"},
			wantInput:  "analyze this",
			wantOutput: "answer\n",
		},
		{
			name:       "stdin json",
			args:       []string{"--output-format", "json", repo},
			stdin:      "proposal from stdin\n",
			wantInput:  "proposal from stdin",
			wantOutput: "{\"schema_version\":\"debate-role/v1\",\"role\":\"debate-proposer\",\"engine\":\"agy\",\"status\":\"success\",\"content\":\"answer\"}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotRequest ProviderRequest
			provider := ProviderFunc(func(_ context.Context, req ProviderRequest) (string, error) {
				gotRequest = req
				return "answer\r\n", nil
			})
			var stdout, stderr bytes.Buffer

			code := Run(tt.args, bytes.NewBufferString(tt.stdin), &stdout, &stderr, Config{
				Role:         "debate-proposer",
				Engine:       "agy",
				SystemPrompt: " SYSTEM \n",
				Provider:     provider,
			})

			if code != 0 {
				t.Fatalf("Run() code = %d, want 0; stderr = %q", code, stderr.String())
			}
			if gotRequest.Repository != repo {
				t.Errorf("provider repository = %q, want %q", gotRequest.Repository, repo)
			}
			if gotRequest.Prompt != wantPrompt(tt.wantInput) {
				t.Errorf("provider prompt = %q, want %q", gotRequest.Prompt, wantPrompt(tt.wantInput))
			}
			if stdout.String() != tt.wantOutput {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantOutput)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func repositoryInputForTest(repository, input string) string {
	return "<<<TARGET_REPOSITORY_BEGIN>>>\n" + repository +
		"\n<<<TARGET_REPOSITORY_END>>>\n\n" +
		"Analyze only the target repository above. Do not reuse files or context from another project.\n\n" +
		"<<<USER_INPUT_BEGIN>>>\n" + input + "\n<<<USER_INPUT_END>>>"
}

func TestCompactContentPreservesContentWithinLimit(t *testing.T) {
	got := compactContent("  concise answer\r\n", 100)
	if got != "concise answer" {
		t.Fatalf("compactContent() = %q, want concise answer", got)
	}
}

func TestCompactContentBoundsUnicodeAndPreservesBothEnds(t *testing.T) {
	const maxChars = 80
	input := "시작-" + strings.Repeat("가", 100) + "-최종결론"
	got := compactContent(input, maxChars)
	if count := utf8.RuneCountInString(got); count > maxChars {
		t.Fatalf("rune count = %d, want <= %d; content=%q", count, maxChars, got)
	}
	if !strings.HasPrefix(got, "시작-") || !strings.HasSuffix(got, "-최종결론") {
		t.Fatalf("compactContent() = %q, want beginning and end", got)
	}
	if !strings.Contains(got, "[출력 길이 제한으로 중간 내용 생략]") {
		t.Fatalf("compactContent() = %q, want omission marker", got)
	}
}

func TestCompactContentWithZeroLimitPreservesLegacyWhitespace(t *testing.T) {
	got := compactContent("  answer  \r\n", 0)
	if got != "  answer  " {
		t.Fatalf("compactContent() = %q, want legacy whitespace", got)
	}
}

func TestRunResolvesRepositoryRootFromNestedPath(t *testing.T) {
	repo := newTestRepository(t)
	nestedDir := filepath.Join(repo, "nested", "directory")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nestedFile := filepath.Join(nestedDir, "proposal.txt")
	if err := os.WriteFile(nestedFile, []byte("proposal"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{nestedDir, nestedFile} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			var gotRepository string
			provider := ProviderFunc(func(_ context.Context, req ProviderRequest) (string, error) {
				gotRepository = req.Repository
				return "answer", nil
			})
			var stdout, stderr bytes.Buffer

			code := Run([]string{path, "prompt"}, bytes.NewReader(nil), &stdout, &stderr, Config{
				Role: "debate-proposer", Engine: "agy", SystemPrompt: "SYSTEM", Provider: provider,
			})

			if code != 0 {
				t.Fatalf("Run() code = %d, want 0; stderr = %q", code, stderr.String())
			}
			if gotRepository != repo {
				t.Errorf("provider repository = %q, want root %q", gotRepository, repo)
			}
		})
	}
}

func TestRunValidation(t *testing.T) {
	repo := newTestRepository(t)
	nonRepo := t.TempDir()

	tests := []struct {
		name     string
		args     []string
		stdin    string
		provider Provider
		wantCode int
	}{
		{
			name:     "missing path",
			provider: successfulProvider("answer"),
			wantCode: 2,
		},
		{
			name:     "path outside git",
			args:     []string{nonRepo, "prompt"},
			provider: successfulProvider("answer"),
			wantCode: 1,
		},
		{
			name:     "invalid output format",
			args:     []string{"--output-format", "yaml", repo, "prompt"},
			provider: successfulProvider("answer"),
			wantCode: 2,
		},
		{
			name:     "empty positional prompt",
			args:     []string{repo, "   "},
			provider: successfulProvider("answer"),
			wantCode: 2,
		},
		{
			name:     "empty stdin prompt",
			args:     []string{repo},
			stdin:    "\r\n",
			provider: successfulProvider("answer"),
			wantCode: 2,
		},
		{
			name:     "nil provider",
			args:     []string{repo, "prompt"},
			wantCode: 1,
		},
		{
			name:     "empty provider response",
			args:     []string{repo, "prompt"},
			provider: successfulProvider("\r\n"),
			wantCode: 1,
		},
		{
			name:     "whitespace-only provider response",
			args:     []string{repo, "prompt"},
			provider: successfulProvider(" \t\r\n"),
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			provider := tt.provider
			if provider != nil {
				wrapped := provider
				provider = ProviderFunc(func(ctx context.Context, req ProviderRequest) (string, error) {
					called = true
					return wrapped.Run(ctx, req)
				})
			}
			var stdout, stderr bytes.Buffer

			code := Run(tt.args, bytes.NewBufferString(tt.stdin), &stdout, &stderr, Config{
				Role:         "debate-proposer",
				Engine:       "agy",
				SystemPrompt: "SYSTEM",
				Provider:     provider,
			})

			if code != tt.wantCode {
				t.Errorf("Run() code = %d, want %d; stderr = %q", code, tt.wantCode, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if stderr.Len() == 0 {
				t.Error("stderr is empty, want validation error")
			}
			if tt.wantCode == 2 && called {
				t.Error("provider was called for invalid usage")
			}
		})
	}
}

func TestRunPreservesProviderProcessExitCode(t *testing.T) {
	repo := newTestRepository(t)
	script := filepath.Join(t.TempDir(), "exit-seven")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	provider := ProviderFunc(func(context.Context, ProviderRequest) (string, error) {
		err := exec.Command(script).Run()
		if err == nil {
			return "", fmt.Errorf("temporary executable unexpectedly succeeded")
		}
		return "", fmt.Errorf("provider failed: %w", err)
	})
	var stdout, stderr bytes.Buffer

	code := Run([]string{repo, "prompt"}, bytes.NewReader(nil), &stdout, &stderr, Config{
		Role:         "debate-proposer",
		Engine:       "agy",
		SystemPrompt: "SYSTEM",
		Provider:     provider,
	})

	if code != 7 {
		t.Errorf("Run() code = %d, want 7; stderr = %q", code, stderr.String())
	}
}

func successfulProvider(response string) Provider {
	return ProviderFunc(func(context.Context, ProviderRequest) (string, error) {
		return response, nil
	})
}

func newTestRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
