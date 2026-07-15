package debatebackground

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/backgrounddebate"
	"zellij-with-codeagent/internal/debaterole"
)

func TestRunJSONKeepsStdoutStructuredAndProgressOnStderr(t *testing.T) {
	repo := testRepository(t)
	output := filepath.Join(t.TempDir(), "result.json")
	runner := &fakeRoleRunner{}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"--topic", "structured output",
		"--rounds", "1",
		"--cwd", repo,
		"--output", output,
		"--output-format", "json",
	}, strings.NewReader("ignored"), &stdout, &stderr, testDependencies(runner, nil))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var printed backgrounddebate.Result
	if err := json.Unmarshal(stdout.Bytes(), &printed); err != nil {
		t.Fatalf("stdout is not exactly one JSON document: %v\nstdout=%q", err, stdout.String())
	}
	if printed.Status != backgrounddebate.StatusSuccess || printed.OutputPath != output {
		t.Fatalf("printed result = %#v", printed)
	}
	saved := decodeResultFile(t, output)
	if !reflect.DeepEqual(saved, printed) {
		t.Fatalf("saved result = %#v, want printed result %#v", saved, printed)
	}
	if strings.Contains(stdout.String(), "saved debate output") || strings.Contains(stdout.String(), "progress") {
		t.Fatalf("stdout contains surrounding text: %q", stdout.String())
	}
	for _, want := range []string{"repository=" + repo, "role=debate-proposer status=started", "role=debate-judge status=completed", "saved debate output to " + output} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, missing %q", stderr.String(), want)
		}
	}
}

func TestRunTextSavesMarkdownBeforePrintingResult(t *testing.T) {
	repo := testRepository(t)
	outputDir := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"--topic", "markdown output",
		"--cwd", repo,
		"--output", outputDir,
	}, nil, &stdout, &stderr, testDependencies(&fakeRoleRunner{}, nil))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	noticeIndex := strings.Index(stdout.String(), "saved debate output to ")
	headingIndex := strings.Index(stdout.String(), "# Background Debate")
	if noticeIndex < 0 || headingIndex < 0 || noticeIndex > headingIndex {
		t.Fatalf("stdout = %q, want save notice before Markdown", stdout.String())
	}
	lineEnd := strings.IndexByte(stdout.String()[noticeIndex:], '\n')
	if lineEnd < 0 {
		t.Fatalf("stdout = %q, want newline after save notice", stdout.String())
	}
	path := strings.TrimSpace(stdout.String()[noticeIndex+len("saved debate output to ") : noticeIndex+lineEnd])
	if filepath.Dir(path) != outputDir || filepath.Ext(path) != ".md" {
		t.Fatalf("output path = %q, want generated Markdown under %q", path, outputDir)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved Markdown: %v", err)
	}
	printed := stdout.String()[headingIndex:]
	if string(saved) != printed {
		t.Fatalf("saved Markdown differs from printed result\nsaved=%q\nprinted=%q", string(saved), printed)
	}
	for _, want := range []string{"## Status", "success", "## Topic", "markdown output", "### Proposer", "proposer answer", "## Final Recommendation", "judge answer"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("Markdown = %q, missing %q", printed, want)
		}
	}
}

func TestRunWarnsAndIgnoresDeprecatedAgentsAndConfig(t *testing.T) {
	repo := testRepository(t)
	runner := &fakeRoleRunner{}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"--topic", "compatibility",
		"--cwd", repo,
		"--output", filepath.Join(t.TempDir(), "result.json"),
		"--output-format", "json",
		"--agents", "legacy-one,legacy-two",
		"--config", "/does/not/exist.yml",
	}, nil, &stdout, &stderr, testDependencies(runner, nil))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: --agents is deprecated and ignored") || !strings.Contains(stderr.String(), "warning: --config is deprecated and ignored") {
		t.Fatalf("stderr = %q, want both compatibility warnings", stderr.String())
	}
	if got, want := runner.roleNames(), []string{backgrounddebate.Proposer.Name, backgrounddebate.Critic.Name, backgrounddebate.Judge.Name}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %#v, want fixed pipeline %#v", got, want)
	}

	// Explicitly supplying the old default values still counts as using the flags.
	runner = &fakeRoleRunner{}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"--topic", "explicit defaults", "--cwd", repo,
		"--output", filepath.Join(t.TempDir(), "result.json"), "--output-format", "json",
		"--agents", "agy,agent,codex", "--config", "",
	}, nil, &stdout, &stderr, testDependencies(runner, nil))
	if code != 0 || !strings.Contains(stderr.String(), "warning: --agents") || !strings.Contains(stderr.String(), "warning: --config") {
		t.Fatalf("explicit defaults: code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunSavesPartialFailureAndReturnsOne(t *testing.T) {
	repo := testRepository(t)
	output := filepath.Join(t.TempDir(), "failure.json")
	exitCode := 7
	runner := &fakeRoleRunner{failRole: backgrounddebate.Critic.Name, failResult: debaterole.Result{
		SchemaVersion: debaterole.SchemaVersion,
		Role:          backgrounddebate.Critic.Name,
		Engine:        backgrounddebate.Critic.Engine,
		Status:        "failed",
		Content:       "critic partial diagnostic",
	}, failErr: &backgrounddebate.RunError{Kind: backgrounddebate.FailureExecution, Message: "critic crashed", ExitCode: &exitCode}}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"--topic", "partial failure", "--cwd", repo,
		"--output", output, "--output-format", "json",
	}, nil, &stdout, &stderr, testDependencies(runner, nil))

	if code != 1 {
		t.Fatalf("run() exit code = %d, want 1; stderr=%q", code, stderr.String())
	}
	printed := decodeResult(t, stdout.Bytes())
	if printed.Status != backgrounddebate.StatusFailed || printed.Failure == nil || printed.Failure.Role != backgrounddebate.Critic.Name || printed.Failure.ExitCode == nil || *printed.Failure.ExitCode != 7 {
		t.Fatalf("printed failure = %#v", printed)
	}
	if len(printed.Rounds) != 1 || printed.Rounds[0].Proposer == nil || printed.Rounds[0].Critic == nil || printed.Rounds[0].Critic.Content != "critic partial diagnostic" || printed.Rounds[0].Judge != nil {
		t.Fatalf("partial rounds = %#v", printed.Rounds)
	}
	if got := runner.roleNames(); !reflect.DeepEqual(got, []string{backgrounddebate.Proposer.Name, backgrounddebate.Critic.Name}) {
		t.Fatalf("roles = %#v, want no judge call", got)
	}
	if saved := decodeResultFile(t, output); !reflect.DeepEqual(saved, printed) {
		t.Fatalf("saved result = %#v, want printed %#v", saved, printed)
	}
}

func TestRunRejectsJSONWithStartCodexWithoutCreatingFile(t *testing.T) {
	repo := testRepository(t)
	output := filepath.Join(t.TempDir(), "must-not-exist.json")
	runner := &fakeRoleRunner{}
	starter := &fakeCodexStarter{}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"--topic", "invalid start", "--cwd", repo, "--output", output,
		"--output-format", "json", "--start-codex",
	}, nil, &stdout, &stderr, testDependencies(runner, starter))

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output stat error = %v, want not exist", err)
	}
	if len(runner.requests) != 0 || len(starter.requests) != 0 || stdout.Len() != 0 {
		t.Fatalf("validation performed side effects: roles=%d starts=%d stdout=%q", len(runner.requests), len(starter.requests), stdout.String())
	}
	if !strings.Contains(stderr.String(), "--start-codex requires --output-format text") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunStartsCodexOnlyAfterSuccessfulTextResult(t *testing.T) {
	repo := testRepository(t)
	output := filepath.Join(t.TempDir(), "result.md")
	starter := &fakeCodexStarter{}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"--topic", "continue", "--cwd", repo, "--output", output,
		"--start-codex", "--codex-bin", "my-codex",
	}, nil, &stdout, &stderr, testDependencies(&fakeRoleRunner{}, starter))

	if code != 0 || len(starter.requests) != 1 {
		t.Fatalf("run(): code=%d starts=%#v stderr=%q", code, starter.requests, stderr.String())
	}
	req := starter.requests[0]
	wantCommand := []string{
		"my-codex", "--cd", repo, "--add-dir", filepath.Dir(output),
		"The completed debate is saved at " + output + ". Read it and continue from the final judge recommendation.",
	}
	if !reflect.DeepEqual(req.Command, wantCommand) || req.CWD != repo || req.PromptFile != output {
		t.Fatalf("start request = %#v, want command %#v", req, wantCommand)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("Codex was started without persisted output: %v", err)
	}

	starter = &fakeCodexStarter{}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"--topic", "fail", "--cwd", repo, "--output", filepath.Join(t.TempDir(), "failed.md"), "--start-codex"}, nil, &stdout, &stderr,
		testDependencies(&fakeRoleRunner{failRole: backgrounddebate.Proposer.Name, failErr: errors.New("no proposal")}, starter))
	if code != 1 || len(starter.requests) != 0 {
		t.Fatalf("failed run: code=%d starts=%#v", code, starter.requests)
	}
}

func TestRunOverallTimeoutCancelsCodexStarter(t *testing.T) {
	repo := testRepository(t)
	output := filepath.Join(t.TempDir(), "result.md")
	starter := &blockingCodexStarter{}
	var stdout, stderr bytes.Buffer

	started := time.Now()
	code := run([]string{
		"--topic", "continue until timeout", "--cwd", repo, "--output", output,
		"--timeout", "50ms", "--start-codex",
	}, nil, &stdout, &stderr, testDependencies(&fakeRoleRunner{}, starter))

	if code != 1 {
		t.Fatalf("run() exit code = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !errors.Is(starter.contextErr, context.DeadlineExceeded) {
		t.Fatalf("starter context error = %v, want deadline exceeded", starter.contextErr)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("run() elapsed = %v, want overall timeout to stop Codex", elapsed)
	}
	if !strings.Contains(stderr.String(), context.DeadlineExceeded.Error()) {
		t.Fatalf("stderr = %q, want Codex deadline diagnostic", stderr.String())
	}
}

func TestRunHelpDocumentsCompatibilityAndOutputFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, nil, &stdout, &stderr, Dependencies{})

	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("run help: code=%d stderr=%q", code, stderr.String())
	}
	for _, want := range []string{
		"Usage: zellij-agent debate-background [options]",
		"--output-format text|json",
		"--agents", "deprecated; accepted and ignored",
		"--config",
		"--start-codex is available only with text output",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
}

type fakeRoleRunner struct {
	requests   []backgrounddebate.RoleRequest
	failRole   string
	failResult debaterole.Result
	failErr    error
}

func (r *fakeRoleRunner) Run(_ context.Context, req backgrounddebate.RoleRequest) (debaterole.Result, error) {
	r.requests = append(r.requests, req)
	if req.Role.Name == r.failRole {
		return r.failResult, r.failErr
	}
	content := strings.TrimPrefix(req.Role.Name, "debate-") + " answer"
	return debaterole.Result{SchemaVersion: debaterole.SchemaVersion, Role: req.Role.Name, Engine: req.Role.Engine, Status: "success", Content: content}, nil
}

func (r *fakeRoleRunner) roleNames() []string {
	names := make([]string, len(r.requests))
	for i, req := range r.requests {
		names[i] = req.Role.Name
	}
	return names
}

type fakeCodexStarter struct {
	requests []CodexStartRequest
}

type blockingCodexStarter struct {
	contextErr error
}

func (s *blockingCodexStarter) Start(ctx context.Context, _ CodexStartRequest) error {
	select {
	case <-ctx.Done():
		s.contextErr = ctx.Err()
		return ctx.Err()
	case <-time.After(300 * time.Millisecond):
		return errors.New("starter context was not canceled")
	}
}

func (s *fakeCodexStarter) Start(_ context.Context, req CodexStartRequest) error {
	s.requests = append(s.requests, req)
	return nil
}

func testDependencies(runner backgrounddebate.RoleRunner, starter CodexStarter) Dependencies {
	return Dependencies{Runner: runner, CodexStarter: starter, Now: func() time.Time {
		return time.Date(2026, 7, 15, 12, 34, 56, 789, time.UTC)
	}}
}

func testRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	return repo
}

func decodeResultFile(t *testing.T, path string) backgrounddebate.Result {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result %q: %v", path, err)
	}
	return decodeResult(t, data)
}

func decodeResult(t *testing.T, data []byte) backgrounddebate.Result {
	t.Helper()
	var result backgrounddebate.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode result: %v\ndata=%q", err, data)
	}
	return result
}
