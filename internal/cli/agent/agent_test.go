package agentcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestRunStartSendsValidatedRequest(t *testing.T) {
	cwd := t.TempDir()
	client := &testClient{response: started("agent-1", "gemini", "agent-1")}
	var stdout, stderr bytes.Buffer

	code := Run(
		[]string{"start", "gemini", "--cwd", cwd, "--socket", "/tmp/agents.sock", "--timeout", "3s", "--", "--model", "gemini-3"},
		strings.NewReader(""), &stdout, &stderr, testFactory(client),
		Config{
			Getwd:  func() (string, error) { return "/unused", nil },
			Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
		},
	)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	want := transport.StartAgentRequest{
		Kind:               "gemini",
		CWD:                cwd,
		Args:               []string{"--model", "gemini-3"},
		SourceSession:      "session-a",
		SourceZellijPaneID: "terminal_2",
	}
	if !reflect.DeepEqual(client.request, want) {
		t.Fatalf("StartAgent request = %#v, want %#v", client.request, want)
	}
	if client.socket != "/tmp/agents.sock" || client.timeout != 3*time.Second {
		t.Fatalf("client options = socket %q timeout %s", client.socket, client.timeout)
	}
	if stdout.String() != "started agent=agent-1 kind=gemini pane=agent-1\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunStartDefaultsCWDFromConfig(t *testing.T) {
	cwd := t.TempDir()
	client := &testClient{response: started("agent-1", "codex", "pane-1")}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"start", "codex"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getwd:  func() (string, error) { return cwd, nil },
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
	})

	if code != 0 || client.request.CWD != cwd {
		t.Fatalf("code=%d request=%#v stderr=%q", code, client.request, stderr.String())
	}
}

func TestRunStartRejectsInvalidInputBeforeCallingClient(t *testing.T) {
	cwd := t.TempDir()
	file := filepath.Join(cwd, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{name: "unsupported kind", args: []string{"start", "aider"}, want: "unsupported agent kind: aider"},
		{name: "missing kind", args: []string{"start"}, want: "start requires <codex|claude|gemini|cursor>"},
		{name: "non-directory cwd", args: []string{"start", "codex", "--cwd", file}, want: "resolve cwd:"},
		{name: "missing session", args: []string{"start", "codex"}, env: map[string]string{"ZELLIJ_SESSION_NAME": "", "ZELLIJ_PANE_ID": "terminal_2"}, want: "ZELLIJ_SESSION_NAME is required"},
		{name: "missing pane", args: []string{"start", "codex"}, env: map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": ""}, want: "ZELLIJ_PANE_ID is required"},
		{name: "non-positive timeout", args: []string{"start", "codex", "--timeout", "0s"}, want: "--timeout must be greater than 0"},
		{name: "unexpected positional", args: []string{"start", "codex", "extra"}, want: "unexpected start argument before --: extra"},
		{name: "unknown option", args: []string{"start", "codex", "--model", "gemini-3"}, want: "unknown start option: --model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &testClient{response: started("agent-1", "codex", "pane-1")}
			var stdout, stderr bytes.Buffer
			env := map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}
			for key, value := range tt.env {
				env[key] = value
			}
			code := Run(tt.args, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
				Getwd:  func() (string, error) { return cwd, nil },
				Getenv: mapGetenv(env),
			})
			if code != 2 {
				t.Fatalf("Run() exit code = %d, want 2; stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
			if client.calls != 0 {
				t.Fatalf("StartAgent calls = %d, want 0", client.calls)
			}
		})
	}
}

func TestRunStartReportsClientError(t *testing.T) {
	client := &testClient{err: errors.New("daemon unavailable")}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"start", "cursor"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getwd:  func() (string, error) { return t.TempDir(), nil },
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
	})

	if code != 1 || !strings.Contains(stderr.String(), "agent start failed via socket") || !strings.Contains(stderr.String(), "daemon unavailable") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunHelpDocumentsStartContract(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"start", "--help"}} {
		var stdout, stderr bytes.Buffer
		code := Run(args, strings.NewReader(""), &stdout, &stderr, nil, Config{})
		if code != 0 {
			t.Fatalf("Run(%#v) exit code = %d, stderr=%q", args, code, stderr.String())
		}
		for _, want := range []string{"codex", "claude", "gemini", "cursor", "default: current working directory", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-skip-permissions", "--yolo --trust", "-- passthrough"} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("Run(%#v) help = %q, missing %q", args, stdout.String(), want)
			}
		}
	}
}

type testClient struct {
	request  transport.StartAgentRequest
	response transport.StartAgentResponse
	err      error
	calls    int
	socket   string
	timeout  time.Duration
}

func (c *testClient) StartAgent(_ context.Context, request transport.StartAgentRequest) (transport.StartAgentResponse, error) {
	c.calls++
	c.request = request
	return c.response, c.err
}

func testFactory(client *testClient) ClientFactory {
	return func(socket string, timeout time.Duration) AgentClient {
		client.socket = socket
		client.timeout = timeout
		return client
	}
}

func mapGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func started(id, kind, pane string) transport.StartAgentResponse {
	return transport.StartAgentResponse{Agent: transport.AgentWithPane{
		Agent: transport.Agent{ID: id, Kind: kind, PaneID: pane},
		Pane:  transport.Pane{ID: pane},
	}}
}
