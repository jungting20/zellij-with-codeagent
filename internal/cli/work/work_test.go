package workcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestRunDryRunPrintsExecutionPlanEnvelope(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--cwd", cwd,
		"--session", "work-command",
		"--zellij-session", "flag-session",
		"--dry-run",
		"implement", "work", "command",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.requestID != "" {
		t.Fatalf("requestID = %q, want no submit during dry-run", client.requestID)
	}
	if got := bytes.Count(stdout.Bytes(), []byte(`"initial_input"`)); got != 1 {
		t.Fatalf("dry-run initial_input keys = %d, want coder-only JSON field", got)
	}
	if got := bytes.Count(stdout.Bytes(), []byte(`"initial_input_ready_text"`)); got != 1 {
		t.Fatalf("dry-run initial_input_ready_text keys = %d, want coder-only JSON field", got)
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_work-command" {
		t.Fatalf("envelope = %#v, want execution_plan req_work-command", envelope)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload.Session != "work-command" || len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 5 {
		t.Fatalf("payload = %#v, want work-command with five panes", payload)
	}
	if payload.ZellijSession != "flag-session" {
		t.Fatalf("payload.ZellijSession = %q, want flag-session", payload.ZellijSession)
	}
	if got := payload.Tabs[0].Panes[0].Command; len(got) != 4 || got[0] != "/tmp/bin/zellij-agent" || got[1] != "role" || got[2] != "coding-agent" || got[3] != cwd {
		t.Fatalf("coder command = %#v, want configured role command", got)
	}
	if got := payload.Tabs[0].Panes[0].InitialInput; got != "implement work command" {
		t.Fatalf("coder InitialInput = %q, want dry-run goal", got)
	}
	if got := payload.Tabs[0].Panes[0].InitialInputReadyText; got != "›" {
		t.Fatalf("coder InitialInputReadyText = %q, want Codex prompt marker", got)
	}
	for _, pane := range payload.Tabs[0].Panes[1:] {
		if pane.InitialInput != "" {
			t.Fatalf("pane %q InitialInput = %q, want coder-only prefill", pane.ID, pane.InitialInput)
		}
		if pane.InitialInputReadyText != "" {
			t.Fatalf("pane %q InitialInputReadyText = %q, want coder-only readiness", pane.ID, pane.InitialInputReadyText)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty dry-run stderr", stderr.String())
	}
}

func TestRunDryRunUsesEnvironmentZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--cwd", t.TempDir(), "--dry-run", "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})
	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("Unmarshal(envelope) error = %v", err)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal(payload) error = %v", err)
	}
	if payload.ZellijSession != "env-session" {
		t.Fatalf("payload.ZellijSession = %q, want env-session", payload.ZellijSession)
	}
}

func TestRunRejectsMissingZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--cwd", t.TempDir(), "--dry-run", "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})
	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "resolve zellij session: zellij session is required") {
		t.Fatalf("stderr = %q, want resolver error", stderr.String())
	}
}

func TestRunSubmitsGeneratedPlan(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "test-session")
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module example.com/demo\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--socket", "/tmp/custom.sock",
		"--timeout", "5s",
		"--cwd", cwd,
		"--session", "work-command",
		"--auto-test",
		"implement work command",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" || client.timeout != 5*time.Second || client.requestID != "req_work-command" {
		t.Fatalf("client socket=%q timeout=%s request=%q, want custom socket timeout request", client.socketPath, client.timeout, client.requestID)
	}
	if client.payload.Session != "work-command" || len(client.payload.Tabs[0].Panes) != 5 {
		t.Fatalf("payload = %#v, want submitted work-command plan", client.payload)
	}
	if !strings.Contains(client.payload.Tabs[0].Panes[1].Command[2], "finished with exit=%s") {
		t.Fatalf("test command = %q, want auto-test marker", client.payload.Tabs[0].Panes[1].Command[2])
	}
	firstLine, _, _ := strings.Cut(stdout.String(), "\n")
	if !strings.Contains(firstLine, "request=req_work-command session=work-command") ||
		!strings.Contains(firstLine, "layout=triple-horizontal") ||
		!strings.Contains(firstLine, "tabs=1") ||
		!strings.Contains(firstLine, "panes=5") ||
		!strings.Contains(stdout.String(), "- coder role=coding-agent") {
		t.Fatalf("stdout = %q, want work summary", stdout.String())
	}
}

func TestRunDryRunDetectsProjectDefaults(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "test-session")
	tests := []struct {
		name       string
		files      map[string]string
		autoTest   bool
		wantTest   string
		wantNotes  []string
		rejectTest string
	}{
		{
			name:     "go",
			files:    map[string]string{"go.mod": "module example.com/demo\n"},
			wantTest: "Suggested test command: go test ./...",
			wantNotes: []string{
				"Profile: go",
				"Markers: go.mod",
				"Build command: go build ./...",
				"Feedback: enabled",
			},
		},
		{
			name: "pnpm",
			files: map[string]string{
				"package.json":   `{"scripts":{"test":"vitest","build":"vite build"}}`,
				"pnpm-lock.yaml": "lockfileVersion: '9.0'\n",
			},
			wantTest: "Suggested test command: pnpm test",
			wantNotes: []string{
				"Profile: pnpm",
				"Markers: package.json, pnpm-lock.yaml",
				"Build command: pnpm build",
				"Feedback: enabled",
			},
		},
		{
			name:     "rust",
			files:    map[string]string{"Cargo.toml": "[package]\nname = \"demo\"\n"},
			wantTest: "Suggested test command: cargo test",
			wantNotes: []string{
				"Profile: rust",
				"Build command: cargo check",
				"Feedback: enabled",
			},
		},
		{
			name:     "unknown",
			files:    map[string]string{},
			wantTest: "Feedback disabled: project type not detected",
			wantNotes: []string{
				"Profile: unknown",
				"Feedback: disabled",
				"Reason: project type not detected",
			},
		},
		{
			name: "mixed",
			files: map[string]string{
				"go.mod":       "module example.com/demo\n",
				"package.json": `{"scripts":{"test":"vitest"}}`,
			},
			wantTest: "Feedback disabled: multiple project families detected",
			wantNotes: []string{
				"Profile: unknown",
				"Markers: go.mod, package.json",
				"Feedback: disabled",
				"Reason: multiple project families detected",
			},
		},
		{
			name:     "node without test script under auto test",
			files:    map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`},
			autoTest: true,
			wantTest: "Feedback disabled: package.json has no test script",
			wantNotes: []string{
				"Profile: npm",
				"Build command: npm run build",
				"Feedback: disabled",
			},
			rejectTest: "npm test",
		},
		{
			name:       "malformed package json under auto test",
			files:      map[string]string{"package.json": `{"scripts":`},
			autoTest:   true,
			wantTest:   "Feedback disabled: invalid package.json",
			wantNotes:  []string{"Profile: npm", "Feedback: disabled", "Reason: invalid package.json"},
			rejectTest: "npm test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := t.TempDir()
			for name, body := range tt.files {
				if err := os.WriteFile(filepath.Join(cwd, name), []byte(body), 0o600); err != nil {
					t.Fatalf("WriteFile(%s) error = %v", name, err)
				}
			}
			client := &fakeAgentClient{}
			var stdout, stderr bytes.Buffer

			args := []string{"--cwd", cwd, "--dry-run"}
			if tt.autoTest {
				args = append(args, "--auto-test")
			}
			args = append(args, "ship it")
			code := Run(args, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{})
			if code != 0 {
				t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
			}
			var envelope transport.RequestEnvelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatalf("Unmarshal(envelope) error = %v", err)
			}
			var payload transport.ExecutionPlanPayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				t.Fatalf("Unmarshal(payload) error = %v", err)
			}
			testScript := payload.Tabs[0].Panes[1].Command[2]
			notesScript := payload.Tabs[0].Panes[4].Command[2]
			if !strings.Contains(testScript, tt.wantTest) {
				t.Fatalf("test script = %q, want substring %q", testScript, tt.wantTest)
			}
			if tt.rejectTest != "" && strings.Contains(testScript, tt.rejectTest) {
				t.Fatalf("test script = %q, reject executable command %q", testScript, tt.rejectTest)
			}
			for _, want := range tt.wantNotes {
				if !strings.Contains(notesScript, want) {
					t.Fatalf("notes script = %q, want substring %q", notesScript, want)
				}
			}
		})
	}
}

func TestRunDryRunAutoTestDoesNotExecuteDetectedCommand(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "test-session")
	cwd := t.TempDir()
	writePath := filepath.Join(cwd, "executed")
	if err := os.WriteFile(filepath.Join(cwd, "package.json"), []byte(`{"scripts":{"test":"touch executed"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	binDir := filepath.Join(cwd, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatalf("Mkdir(bin) error = %v", err)
	}
	npmPath := filepath.Join(binDir, "npm")
	script := "#!/bin/sh\ntouch " + writePath + "\n"
	if err := os.WriteFile(npmPath, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile(npm) error = %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--cwd", cwd, "--dry-run", "--auto-test", "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})
	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(writePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("detected command side effect exists or stat failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "npm") || !strings.Contains(stdout.String(), "test") {
		t.Fatalf("dry-run output = %q, want encoded npm test command", stdout.String())
	}
}

func TestRunHelpPrintsUsageToStdout(t *testing.T) {
	tests := [][]string{
		{"--help"},
		{"-h"},
		{"help"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run(args, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

			if code != 0 {
				t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage: zellij-agent work") ||
				!strings.Contains(stdout.String(), "--zellij-session string") ||
				!strings.Contains(stdout.String(), "--socket") ||
				!strings.Contains(stdout.String(), "--timeout") ||
				!strings.Contains(stdout.String(), "default 15s") ||
				!strings.Contains(stdout.String(), "run the detected project test command once") {
				t.Fatalf("stdout = %q, want work usage with common options", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty help stderr", stderr.String())
			}
		})
	}
}

func TestRunParseErrorReturnsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--bogus"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 2 {
		t.Fatalf("Run() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want parse error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty parse-error stdout", stdout.String())
	}
}

func TestRunRejectsEmptyGoal(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--cwd", t.TempDir()}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 2 {
		t.Fatalf("Run() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "work requires a goal") {
		t.Fatalf("stderr = %q, want missing goal message", stderr.String())
	}
}

func TestRunRejectsInvalidCWD(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing")

	code := Run([]string{"--cwd", missing, "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "resolve cwd") {
		t.Fatalf("stderr = %q, want cwd error", stderr.String())
	}
}

func TestRunSubmitErrorIncludesDaemonHint(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "test-session")
	cwd := t.TempDir()
	client := &fakeAgentClient{submitErr: errors.New("dial unix /tmp/agentd.sock: connect: no such file")}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--cwd", cwd, "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{})

	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "work submit failed via socket") || !strings.Contains(stderr.String(), "zellij-agent daemon serve") {
		t.Fatalf("stderr = %q, want submit error and daemon hint", stderr.String())
	}
}

func TestResolveCWDReturnsAbsoluteDirectory(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(old)
	})

	got, err := resolveCWD("")
	if err != nil {
		t.Fatalf("resolveCWD() error = %v", err)
	}
	if got != dir {
		t.Fatalf("resolveCWD() = %q, want %q", got, dir)
	}
}

func fakeFactory(client *fakeAgentClient) ClientFactory {
	return func(socketPath string, timeout time.Duration) AgentClient {
		client.socketPath = socketPath
		client.timeout = timeout
		return client
	}
}

type fakeAgentClient struct {
	socketPath string
	timeout    time.Duration
	requestID  string
	payload    transport.ExecutionPlanPayload
	submitErr  error
}

func (c *fakeAgentClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	if c.submitErr != nil {
		return transport.ExecutionPlanResponse{}, c.submitErr
	}
	c.requestID = requestID
	c.payload = payload
	return transport.ExecutionPlanResponse{
		RequestID: requestID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs: []transport.ExecutionPlanTabResponse{
			{
				Name: payload.Tabs[0].Name,
				Panes: []transport.Pane{
					{ID: "coder", Role: "coding-agent", Status: "running", ZellijPaneID: "terminal_1"},
					{ID: "test", Role: "test-runner", Status: "running", ZellijPaneID: "terminal_2"},
					{ID: "review", Role: "review-assistant", Status: "running", ZellijPaneID: "terminal_3"},
					{ID: "lazygit", Role: "lazygit", Status: "running", ZellijPaneID: "terminal_4"},
					{ID: "notes", Role: "notes", Status: "running", ZellijPaneID: "terminal_5"},
				},
			},
		},
	}, nil
}
