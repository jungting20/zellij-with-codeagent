package agentcli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/agentdashboard"
	"zellij-with-codeagent/internal/transport"
)

func TestRunDashboardPassesZellijContextAndOptions(t *testing.T) {
	client := &testClient{}
	var gotOptions agentdashboard.Options
	run := false
	var stdout, stderr bytes.Buffer
	code := Run(
		[]string{"dashboard", "--socket", "/tmp/dashboard.sock", "--timeout", "3s", "--refresh-interval", "4s"},
		strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
			Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": " session-a ", "ZELLIJ_PANE_ID": " terminal_7 "}),
			NewModel: func(_ context.Context, got agentdashboard.Client, opts agentdashboard.Options) tea.Model {
				if got != client {
					t.Fatal("dashboard received a different client")
				}
				gotOptions = opts
				return stubModel{}
			},
			RunProgram: func(context.Context, tea.Model, io.Reader, io.Writer) error {
				run = true
				return nil
			},
		},
	)
	if code != 0 || !run || stderr.Len() != 0 {
		t.Fatalf("code=%d run=%t stderr=%q", code, run, stderr.String())
	}
	if client.socket != "/tmp/dashboard.sock" || client.timeout != 3*time.Second {
		t.Fatalf("client socket=%q timeout=%s", client.socket, client.timeout)
	}
	want := agentdashboard.Options{RefreshInterval: 4 * time.Second, SourceSession: "session-a", SourceZellijPaneID: "terminal_7"}
	if gotOptions != want {
		t.Fatalf("options=%#v, want %#v", gotOptions, want)
	}
}

func TestRunDashboardRequiresZellijContext(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"dashboard"}, strings.NewReader(""), &stdout, &stderr, testFactory(&testClient{}), Config{Getenv: func(string) string { return "" }})
	if code != 2 || !strings.Contains(stderr.String(), "must run inside a Zellij pane") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

type stubModel struct{}

func (stubModel) Init() tea.Cmd                         { return nil }
func (m stubModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (stubModel) View() string                          { return "" }

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

func TestRunStartRequiresInjectedRuntimeDependencies(t *testing.T) {
	cwd := t.TempDir()
	tests := []struct {
		name      string
		args      []string
		cfg       Config
		wantCode  int
		wantError string
	}{
		{
			name: "missing getwd",
			args: []string{"start", "codex"},
			cfg: Config{Getenv: func(string) string {
				t.Fatal("Getenv must not be called when Getwd is missing")
				return ""
			}},
			wantCode:  1,
			wantError: "agent start configuration error: Getwd is required",
		},
		{
			name:      "missing getenv",
			args:      []string{"start", "codex"},
			cfg:       Config{Getwd: func() (string, error) { return cwd, nil }},
			wantCode:  1,
			wantError: "agent start configuration error: Getenv is required",
		},
		{
			name: "getwd failure",
			args: []string{"start", "codex"},
			cfg: Config{
				Getwd: func() (string, error) { return "", errors.New("working directory unavailable") },
				Getenv: func(string) string {
					t.Fatal("Getenv must not be called when Getwd fails")
					return ""
				},
			},
			wantCode:  1,
			wantError: "determine working directory: working directory unavailable",
		},
		{
			name: "explicit invalid cwd is usage error",
			args: []string{"start", "codex", "--cwd", filepath.Join(cwd, "missing")},
			cfg: Config{
				Getwd:  func() (string, error) { return cwd, nil },
				Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
			},
			wantCode:  2,
			wantError: "resolve cwd:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &testClient{response: started("agent-1", "codex", "pane-1")}
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, strings.NewReader(""), &stdout, &stderr, testFactory(client), tt.cfg)
			if code != tt.wantCode || !strings.Contains(stderr.String(), tt.wantError) {
				t.Fatalf("code=%d stderr=%q, want code=%d and %q", code, stderr.String(), tt.wantCode, tt.wantError)
			}
			if stdout.Len() != 0 || client.calls != 0 {
				t.Fatalf("stdout=%q calls=%d, want empty and zero", stdout.String(), client.calls)
			}
		})
	}
}

func TestRunStartRejectsMissingClientWithoutPanic(t *testing.T) {
	cfg := Config{
		Getwd:  func() (string, error) { return t.TempDir(), nil },
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
	}
	tests := []struct {
		name    string
		factory ClientFactory
	}{
		{name: "nil factory"},
		{name: "nil client", factory: func(string, time.Duration) AgentClient { return nil }},
		{name: "typed nil client", factory: func(string, time.Duration) AgentClient {
			var client *testClient
			return client
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{"start", "codex"}, strings.NewReader(""), &stdout, &stderr, tt.factory, cfg)
			if code != 1 || !strings.Contains(stderr.String(), "agent start client is not configured") || stdout.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunStartAcceptsOnlyExactKinds(t *testing.T) {
	cwd := t.TempDir()
	cfg := Config{
		Getwd:  func() (string, error) { return cwd, nil },
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": " session-a ", "ZELLIJ_PANE_ID": " terminal_2 "}),
	}
	for _, kind := range []string{"codex", "claude", "gemini", "cursor"} {
		t.Run(kind, func(t *testing.T) {
			client := &testClient{response: started("agent-1", kind, "pane-1")}
			var stdout, stderr bytes.Buffer
			code := Run([]string{"start", kind}, strings.NewReader(""), &stdout, &stderr, testFactory(client), cfg)
			if code != 0 || client.request.Kind != kind || client.request.SourceSession != "session-a" || client.request.SourceZellijPaneID != "terminal_2" {
				t.Fatalf("code=%d request=%#v stderr=%q", code, client.request, stderr.String())
			}
		})
	}
	for _, invalid := range []string{"Codex", "CLAUDE", "GEMINI", "Cursor", "agy", "agent", "claude-code"} {
		t.Run("reject "+invalid, func(t *testing.T) {
			client := &testClient{}
			var stdout, stderr bytes.Buffer
			code := Run([]string{"start", invalid}, strings.NewReader(""), &stdout, &stderr, testFactory(client), cfg)
			if code != 2 || !strings.Contains(stderr.String(), "unsupported agent kind") || stdout.Len() != 0 || client.calls != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q calls=%d", code, stdout.String(), stderr.String(), client.calls)
			}
		})
	}
}

func TestRunStartParsesStrictOptionsAndPassthrough(t *testing.T) {
	cwd := t.TempDir()
	link := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(cwd, link); err != nil {
		t.Fatal(err)
	}
	client := &testClient{response: started("agent-1", "gemini", "pane-1")}
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"start", "gemini", "--timeout=1s", "--socket=/first.sock", "--cwd", link,
		"--socket", "/last.sock", "--timeout", "2s", "--", "--model", "gemini-3", "--", "--unsafe",
	}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getwd:  func() (string, error) { return "/unused", nil },
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
	})

	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if client.socket != "/last.sock" || client.timeout != 2*time.Second || client.request.CWD != link {
		t.Fatalf("options socket=%q timeout=%s cwd=%q", client.socket, client.timeout, client.request.CWD)
	}
	if !reflect.DeepEqual(client.request.Args, []string{"--model", "gemini-3", "--", "--unsafe"}) {
		t.Fatalf("passthrough=%#v", client.request.Args)
	}
	if !client.hasDeadline || time.Until(client.deadline) <= 0 || time.Until(client.deadline) > 2*time.Second {
		t.Fatalf("deadline=%s hasDeadline=%t", client.deadline, client.hasDeadline)
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
			if client.calls != 0 || stdout.Len() != 0 {
				t.Fatalf("StartAgent calls = %d stdout=%q, want zero and empty", client.calls, stdout.String())
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

	if code != 1 || !strings.Contains(stderr.String(), "agent start failed via socket") || !strings.Contains(stderr.String(), "daemon unavailable") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunHelpDocumentsStartContract(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"start", "--help"}} {
		var stdout, stderr bytes.Buffer
		code := Run(args, strings.NewReader(""), &stdout, &stderr, nil, Config{})
		if code != 0 {
			t.Fatalf("Run(%#v) exit code = %d, stderr=%q", args, code, stderr.String())
		}
		for _, want := range []string{"codex", "claude", "gemini", "cursor", "agy", "default: current working directory", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-skip-permissions", "agent --yolo --trust", "-- passthrough"} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("Run(%#v) help = %q, missing %q", args, stdout.String(), want)
			}
		}
		for _, unwanted := range []string{"list", "stop"} {
			if strings.Contains(stdout.String(), unwanted) {
				t.Fatalf("Run(%#v) help = %q, unexpectedly contains %q", args, stdout.String(), unwanted)
			}
		}
	}
}

type testClient struct {
	request     transport.StartAgentRequest
	response    transport.StartAgentResponse
	err         error
	calls       int
	socket      string
	timeout     time.Duration
	deadline    time.Time
	hasDeadline bool
}

func (c *testClient) StartAgent(ctx context.Context, request transport.StartAgentRequest) (transport.StartAgentResponse, error) {
	c.calls++
	c.request = request
	c.deadline, c.hasDeadline = ctx.Deadline()
	return c.response, c.err
}

func (c *testClient) ListAgents(context.Context) (transport.ListAgentsResponse, error) {
	return transport.ListAgentsResponse{}, nil
}

func (c *testClient) FocusAgent(context.Context, string, transport.FocusAgentRequest) (transport.FocusAgentResponse, error) {
	return transport.FocusAgentResponse{}, nil
}

func (c *testClient) StreamEvents(context.Context) (*transport.EventStream, error) {
	return &transport.EventStream{}, nil
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
