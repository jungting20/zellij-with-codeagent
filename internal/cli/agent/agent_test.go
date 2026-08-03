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
			Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": " session-a ", "ZELLIJ_PANE_ID": " 7 "}),
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

func TestRunNextFocusesNextAgentWithZellijContext(t *testing.T) {
	client := &testClient{nextResponse: focusedNext("agent-2", "claude", "agent-2", "pane-2")}
	var stdout, stderr bytes.Buffer

	code := Run(
		[]string{"next", "--socket", "/tmp/next.sock", "--timeout", "3s"},
		strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
			Getenv: mapGetenv(map[string]string{
				"ZELLIJ_SESSION_NAME": " session-b ",
				"ZELLIJ_PANE_ID":      " 8 ",
			}),
		},
	)

	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if client.socket != "/tmp/next.sock" || client.timeout != 3*time.Second {
		t.Fatalf("client socket=%q timeout=%s", client.socket, client.timeout)
	}
	want := transport.FocusNextAgentRequest{SourceSession: "session-b", SourceZellijPaneID: "terminal_8"}
	if !reflect.DeepEqual(client.nextRequest, want) {
		t.Fatalf("FocusNextAgent request=%#v, want %#v", client.nextRequest, want)
	}
	if !client.nextHasDeadline || time.Until(client.nextDeadline) <= 0 || time.Until(client.nextDeadline) > 3*time.Second {
		t.Fatalf("deadline=%s hasDeadline=%t", client.nextDeadline, client.nextHasDeadline)
	}
	if stdout.String() != "focused agent=agent-2 kind=claude pane=agent-2\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if client.nextCalls != 1 {
		t.Fatalf("FocusNextAgent calls=%d, want 1", client.nextCalls)
	}
}

func TestRunNextFallsBackToPaneID(t *testing.T) {
	client := &testClient{nextResponse: focusedNext("agent-2", "claude", "", "pane-2")}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"next"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-b", "ZELLIJ_PANE_ID": "terminal_8"}),
	})

	if code != 0 || stderr.Len() != 0 || stdout.String() != "focused agent=agent-2 kind=claude pane=pane-2\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if client.nextCalls != 1 {
		t.Fatalf("FocusNextAgent calls=%d, want 1", client.nextCalls)
	}
}

func TestRunNextSilentlySucceedsWhenNoIdleAgentExists(t *testing.T) {
	client := &testClient{nextResponse: transport.FocusNextAgentResponse{Focused: false}}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"next"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getenv: mapGetenv(map[string]string{
			"ZELLIJ_SESSION_NAME": "session-b",
			"ZELLIJ_PANE_ID":      "terminal_8",
		}),
	})
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 || client.nextCalls != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q calls=%d", code, stdout.String(), stderr.String(), client.nextCalls)
	}
}

func TestRunNextRejectsInvalidConfigurationAndInput(t *testing.T) {
	validConfig := Config{Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-b", "ZELLIJ_PANE_ID": "terminal_8"})}
	tests := []struct {
		name           string
		args           []string
		cfg            Config
		factory        ClientFactory
		concreteClient bool
		code           int
		want           string
	}{
		{name: "positional argument", args: []string{"next", "extra"}, cfg: validConfig, concreteClient: true, code: 2, want: "agent next does not accept positional arguments"},
		{name: "non-positive timeout", args: []string{"next", "--timeout", "0s"}, cfg: validConfig, concreteClient: true, code: 2, want: "agent next --timeout must be positive"},
		{name: "missing getenv", args: []string{"next"}, cfg: Config{}, concreteClient: true, code: 1, want: "agent next configuration error: Getenv is required"},
		{name: "missing zellij session", args: []string{"next"}, cfg: Config{Getenv: mapGetenv(map[string]string{"ZELLIJ_PANE_ID": "terminal_8"})}, concreteClient: true, code: 2, want: "agent next must run inside a Zellij pane"},
		{name: "missing zellij pane", args: []string{"next"}, cfg: Config{Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-b"})}, concreteClient: true, code: 2, want: "agent next must run inside a Zellij pane"},
		{name: "nil factory", args: []string{"next"}, cfg: validConfig, code: 1, want: "agent next client is not configured"},
		{name: "nil client", args: []string{"next"}, cfg: validConfig, factory: func(string, time.Duration) AgentClient { return nil }, code: 1, want: "agent next client is not configured"},
		{name: "typed nil client", args: []string{"next"}, cfg: validConfig, factory: func(string, time.Duration) AgentClient { var client *testClient; return client }, code: 1, want: "agent next client is not configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := tt.factory
			var client *testClient
			if tt.concreteClient {
				client = &testClient{}
				factory = testFactory(client)
			}
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, strings.NewReader(""), &stdout, &stderr, factory, tt.cfg)
			if code != tt.code || !strings.Contains(stderr.String(), tt.want) || stdout.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if client != nil && client.nextCalls != 0 {
				t.Fatalf("FocusNextAgent calls=%d, want 0", client.nextCalls)
			}
		})
	}
}

func TestRunNextReportsClientError(t *testing.T) {
	client := &testClient{nextErr: errors.New("daemon unavailable")}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"next"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-b", "ZELLIJ_PANE_ID": "terminal_8"}),
	})

	if code != 1 || !strings.Contains(stderr.String(), "agent next failed via socket") || !strings.Contains(stderr.String(), "daemon unavailable") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if client.nextCalls != 1 {
		t.Fatalf("FocusNextAgent calls=%d, want 1", client.nextCalls)
	}
}

func TestRunHelpDocumentsNextContract(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{args: []string{"--help"}, want: []string{"next"}},
		{args: []string{"next", "--help"}, want: []string{"next", "--socket PATH", "--timeout DURATION"}},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		code := Run(tt.args, strings.NewReader(""), &stdout, &stderr, nil, Config{})
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("Run(%#v): code=%d stderr=%q", tt.args, code, stderr.String())
		}
		for _, want := range tt.want {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("Run(%#v) help=%q, missing %q", tt.args, stdout.String(), want)
			}
		}
	}
}

type stubModel struct{}

func (stubModel) Init() tea.Cmd                         { return nil }
func (m stubModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (stubModel) View() string                          { return "" }

func TestRunStartSendsValidatedRequest(t *testing.T) {
	cwd := t.TempDir()
	command := []string{"agy", "--dangerously-skip-permissions", "--model", "gemini-3"}
	client := &testClient{response: started("agent-1", "gemini", "agent-1", command, cwd)}
	stdin := strings.NewReader("interactive input")
	var stdout, stderr bytes.Buffer
	var gotCommand []string
	var gotCWD string
	var gotStdin io.Reader
	var gotStdout, gotStderr io.Writer

	code := Run(
		[]string{"start", "gemini", "--cwd", cwd, "--socket", "/tmp/agents.sock", "--timeout", "3s", "--", "--model", "gemini-3"},
		stdin, &stdout, &stderr, testFactory(client),
		Config{
			Getwd:  func() (string, error) { return "/unused", nil },
			Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "2"}),
			RunAgent: func(command []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) error {
				select {
				case <-client.startContext.Done():
				default:
					t.Fatal("registration context is not canceled before runner starts")
				}
				gotCommand = append([]string(nil), command...)
				gotCWD = cwd
				gotStdin, gotStdout, gotStderr = stdin, stdout, stderr
				return nil
			},
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
	if !reflect.DeepEqual(gotCommand, command) || gotCWD != cwd {
		t.Fatalf("runner command=%#v cwd=%q, want command=%#v cwd=%q", gotCommand, gotCWD, command, cwd)
	}
	if gotStdin != stdin || gotStdout != &stdout || gotStderr != &stderr {
		t.Fatalf("runner stdio was not passed through unchanged")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no start status output", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if client.closeCalls != 1 || client.closePaneID != "agent-1" {
		t.Fatalf("ClosePane calls=%d pane=%q, want one close for agent-1", client.closeCalls, client.closePaneID)
	}
	if !client.closeHasDeadline || !client.closeDeadline.After(client.closeCalledAt) || client.closeContextErr != nil {
		t.Fatalf("close context deadline=%s calledAt=%s hasDeadline=%t err=%v", client.closeDeadline, client.closeCalledAt, client.closeHasDeadline, client.closeContextErr)
	}
}

func TestRunStartDoesNotRunOrCloseWhenRegistrationFails(t *testing.T) {
	client := &testClient{err: errors.New("daemon unavailable")}
	runCalls := 0
	var stdout, stderr bytes.Buffer

	code := Run([]string{"start", "codex"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getwd:  func() (string, error) { return t.TempDir(), nil },
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
		RunAgent: func([]string, string, io.Reader, io.Writer, io.Writer) error {
			runCalls++
			return nil
		},
	})

	if code != 1 || runCalls != 0 || client.closeCalls != 0 {
		t.Fatalf("code=%d runCalls=%d closeCalls=%d stderr=%q", code, runCalls, client.closeCalls, stderr.String())
	}
}

func TestRunStartClosesClaimedPaneAfterRunnerError(t *testing.T) {
	cwd := t.TempDir()
	client := &testClient{response: started("agent-1", "codex", "logical-pane-1", []string{"codex"}, cwd)}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"start", "codex"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getwd:  func() (string, error) { return cwd, nil },
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
		RunAgent: func([]string, string, io.Reader, io.Writer, io.Writer) error {
			return errors.New("agent exited with status 7")
		},
	})

	if code != 1 || !strings.Contains(stderr.String(), "agent exited with status 7") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if client.closeCalls != 1 || client.closePaneID != "logical-pane-1" {
		t.Fatalf("ClosePane calls=%d pane=%q", client.closeCalls, client.closePaneID)
	}
}

func TestRunStartClosesClaimedPaneAfterDefaultRunnerFailures(t *testing.T) {
	cwd := t.TempDir()
	tests := []struct {
		name    string
		command []string
	}{
		{name: "empty command"},
		{name: "empty executable", command: []string{"  "}},
		{name: "startup failure", command: []string{filepath.Join(cwd, "missing-agent")}},
		{name: "nonzero exit", command: []string{"/usr/bin/false"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &testClient{response: started("agent-1", "codex", "logical-pane-1", tt.command, cwd)}
			var stdout, stderr bytes.Buffer

			code := Run([]string{"start", "codex"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
				Getwd:  func() (string, error) { return cwd, nil },
				Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
			})

			if code != 1 || stderr.Len() == 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			if client.closeCalls != 1 || client.closePaneID != "logical-pane-1" {
				t.Fatalf("ClosePane calls=%d pane=%q", client.closeCalls, client.closePaneID)
			}
		})
	}
}

func TestRunStartReportsCloseError(t *testing.T) {
	cwd := t.TempDir()
	client := &testClient{
		response: started("agent-1", "codex", "logical-pane-1", []string{"codex"}, cwd),
		closeErr: errors.New("close unavailable"),
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"start", "codex"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getwd:  func() (string, error) { return cwd, nil },
		Getenv: mapGetenv(map[string]string{"ZELLIJ_SESSION_NAME": "session-a", "ZELLIJ_PANE_ID": "terminal_2"}),
		RunAgent: func([]string, string, io.Reader, io.Writer, io.Writer) error {
			return nil
		},
	})

	if code != 1 || !strings.Contains(stderr.String(), "close unavailable") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if client.closeCalls != 1 || client.closePaneID != "logical-pane-1" {
		t.Fatalf("ClosePane calls=%d pane=%q", client.closeCalls, client.closePaneID)
	}
}

func TestRunStartDefaultsCWDFromConfig(t *testing.T) {
	cwd := t.TempDir()
	client := &testClient{response: started("agent-1", "codex", "pane-1", []string{"/usr/bin/true"}, cwd)}
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
			client := &testClient{response: started("agent-1", "codex", "pane-1", []string{"/usr/bin/true"}, cwd)}
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
			client := &testClient{response: started("agent-1", kind, "pane-1", []string{"/usr/bin/true"}, cwd)}
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
	client := &testClient{response: started("agent-1", "gemini", "pane-1", []string{"/usr/bin/true"}, cwd)}
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
			client := &testClient{response: started("agent-1", "codex", "pane-1", []string{"/usr/bin/true"}, cwd)}
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
		for _, want := range []string{"codex", "claude", "gemini", "cursor", "agy", "default: current working directory", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-skip-permissions", "agent --yolo --trust", "-- passthrough", "current Zellij pane", "closes the managed pane when the agent exits"} {
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
	request          transport.StartAgentRequest
	response         transport.StartAgentResponse
	err              error
	calls            int
	nextRequest      transport.FocusNextAgentRequest
	nextResponse     transport.FocusNextAgentResponse
	nextErr          error
	nextCalls        int
	socket           string
	timeout          time.Duration
	deadline         time.Time
	hasDeadline      bool
	nextDeadline     time.Time
	nextHasDeadline  bool
	startContext     context.Context
	closePaneID      string
	closeCalls       int
	closeErr         error
	closeDeadline    time.Time
	closeHasDeadline bool
	closeCalledAt    time.Time
	closeContextErr  error
}

func (c *testClient) StartAgent(ctx context.Context, request transport.StartAgentRequest) (transport.StartAgentResponse, error) {
	c.calls++
	c.request = request
	c.startContext = ctx
	c.deadline, c.hasDeadline = ctx.Deadline()
	return c.response, c.err
}

func (c *testClient) ClosePane(ctx context.Context, paneID string) (transport.ClosePaneResponse, error) {
	c.closeCalls++
	c.closePaneID = paneID
	c.closeCalledAt = time.Now()
	c.closeDeadline, c.closeHasDeadline = ctx.Deadline()
	c.closeContextErr = ctx.Err()
	return transport.ClosePaneResponse{}, c.closeErr
}

func (c *testClient) ListAgents(context.Context) (transport.ListAgentsResponse, error) {
	return transport.ListAgentsResponse{}, nil
}

func (c *testClient) FocusAgent(context.Context, string, transport.FocusAgentRequest) (transport.FocusAgentResponse, error) {
	return transport.FocusAgentResponse{}, nil
}

func (c *testClient) FocusNextAgent(ctx context.Context, request transport.FocusNextAgentRequest) (transport.FocusNextAgentResponse, error) {
	c.nextCalls++
	c.nextRequest = request
	c.nextDeadline, c.nextHasDeadline = ctx.Deadline()
	return c.nextResponse, c.nextErr
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

func focusedNext(id, kind, agentPaneID, paneID string) transport.FocusNextAgentResponse {
	return transport.FocusNextAgentResponse{Focused: true, Agent: transport.AgentWithPane{
		Agent: transport.Agent{ID: id, Kind: kind, PaneID: agentPaneID},
		Pane:  transport.Pane{ID: paneID},
	}}
}

func started(id, kind, pane string, command []string, cwd string) transport.StartAgentResponse {
	return transport.StartAgentResponse{Agent: transport.AgentWithPane{
		Agent: transport.Agent{ID: id, Kind: kind, PaneID: pane},
		Pane:  transport.Pane{ID: pane, Command: append([]string(nil), command...), CWD: cwd},
	}}
}
