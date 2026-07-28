package ticketmanager

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

func TestParseOptionsRequiresPathTaskAndAnchor(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
	for name, args := range map[string][]string{
		"path":   {"--task", "tickets", "--anchor-pane", "manager"},
		"task":   {"--anchor-pane", "manager", "/repo"},
		"anchor": {"--task", "tickets", "/repo"},
		"extra":  {"--task", "tickets", "--anchor-pane", "manager", "/repo", "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOptions(args, &bytes.Buffer{}); err == nil {
				t.Fatalf("parseOptions(%q) error = nil", args)
			}
		})
	}
}

func TestParseOptionsDefaultsAndExplicitValues(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	defaults, err := parseOptions([]string{"--task", "tickets", "--anchor-pane", "manager", "/repo"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.RoleBin != "zellij-agent" || defaults.StartupTimeout != 15*time.Second || defaults.ZellijSession != "env-session" || defaults.SocketPath == "" {
		t.Fatalf("defaults = %+v", defaults)
	}

	explicit, err := parseOptions([]string{
		"--socket", "/tmp/custom.sock", "--task", "task-a", "--anchor-pane", "anchor-a",
		"--zellij-session", "physical-a", "--role-bin", "/bin/role", "--startup-timeout", "3s", "/repo",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.SocketPath != "/tmp/custom.sock" || explicit.TaskID != "task-a" || explicit.AnchorPaneID != "anchor-a" || explicit.ZellijSession != "physical-a" || explicit.RoleBin != "/bin/role" || explicit.StartupTimeout != 3*time.Second || explicit.Path != "/repo" {
		t.Fatalf("explicit = %+v", explicit)
	}
}

func TestParseOptionsRejectsNonPositiveStartupTimeout(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
	if _, err := parseOptions([]string{"--task", "tickets", "--anchor-pane", "manager", "--startup-timeout", "0s", "/repo"}, &bytes.Buffer{}); err == nil {
		t.Fatal("parseOptions() error = nil")
	}
}

func TestRunWithDependenciesWiresProjectConfigStoreClientAndManager(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ticketworker.InitializeProject(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "internal", "feature")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	client := &fakeRoleClient{}
	notifier := &fakeVoiceNotifier{}
	var notifierOutput io.Writer
	var clientOptions transport.ClientOptions
	var managerOptions ticketworker.ManagerOptions
	runner := &fakeRunner{}
	deps := defaultDependencies()
	deps.newClient = func(opts transport.ClientOptions) ticketworker.ManagerClient {
		clientOptions = opts
		return client
	}
	deps.newVoiceNotifier = func(output io.Writer) ticketworker.VoiceNotifier {
		notifierOutput = output
		return notifier
	}
	deps.newManager = func(opts ticketworker.ManagerOptions) (managerRunner, error) {
		managerOptions = opts
		return runner, nil
	}
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{
		"--socket", "/tmp/tickets.sock", "--task", "tickets", "--anchor-pane", "ticket-manager",
		"--zellij-session", "physical-a", "--role-bin", "custom-agent", "--startup-timeout", "2s", nested,
	}, &stdout, &stderr, deps)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if clientOptions.SocketPath != "/tmp/tickets.sock" {
		t.Fatalf("client options = %+v", clientOptions)
	}
	if managerOptions.Root != root || managerOptions.TaskID != "tickets" || managerOptions.AnchorPaneID != "ticket-manager" || managerOptions.ZellijSession != "physical-a" || managerOptions.RoleBin != "custom-agent" || managerOptions.StartupTimeout != 2*time.Second {
		t.Fatalf("manager options = %+v", managerOptions)
	}
	if managerOptions.Store == nil || managerOptions.Client != client || managerOptions.Config.MaxWorkers != 3 {
		t.Fatalf("manager dependencies = %+v", managerOptions)
	}
	if !managerOptions.Config.VoiceNotifications || managerOptions.Config.VoiceNotificationPrefix != "ticket-manager" {
		t.Fatalf("voice config = enabled:%v prefix:%q", managerOptions.Config.VoiceNotifications, managerOptions.Config.VoiceNotificationPrefix)
	}
	if notifierOutput != &stdout || managerOptions.VoiceNotifier != notifier {
		t.Fatalf("voice wiring output=%T notifier=%T", notifierOutput, managerOptions.VoiceNotifier)
	}
	if !runner.ran {
		t.Fatal("manager Run was not called")
	}
}

func TestRunWithDependenciesSkipsVoiceNotifierWhenDisabled(t *testing.T) {
	root := initializedTicketManagerProject(t)
	config := "version: 1\nmax_workers: 3\npoll_interval: 30s\nvoice_notifications: false\nvoice_notification_prefix: ticket-manager\n"
	if err := os.WriteFile(ticketworker.ConfigPath(root), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	var factoryCalls int
	var managerOptions ticketworker.ManagerOptions
	deps := defaultDependencies()
	deps.newClient = func(transport.ClientOptions) ticketworker.ManagerClient { return &fakeRoleClient{} }
	deps.newVoiceNotifier = func(io.Writer) ticketworker.VoiceNotifier {
		factoryCalls++
		return &fakeVoiceNotifier{}
	}
	deps.newManager = func(opts ticketworker.ManagerOptions) (managerRunner, error) {
		managerOptions = opts
		return &fakeRunner{}, nil
	}

	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{
		"--task", "tickets", "--anchor-pane", "ticket-manager", "--zellij-session", "physical-a", root,
	}, &stdout, &stderr, deps)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if factoryCalls != 0 || managerOptions.VoiceNotifier != nil {
		t.Fatalf("disabled voice wiring factory calls=%d notifier=%T", factoryCalls, managerOptions.VoiceNotifier)
	}
}

func TestRunWithDependenciesClosesVoiceNotifierWhenManagerConstructionFails(t *testing.T) {
	root := initializedTicketManagerProject(t)
	notifier := &fakeVoiceNotifier{}
	deps := defaultDependencies()
	deps.newClient = func(transport.ClientOptions) ticketworker.ManagerClient { return &fakeRoleClient{} }
	deps.newVoiceNotifier = func(io.Writer) ticketworker.VoiceNotifier { return notifier }
	deps.newManager = func(ticketworker.ManagerOptions) (managerRunner, error) {
		return nil, errors.New("invalid manager")
	}

	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{
		"--task", "tickets", "--anchor-pane", "ticket-manager", "--zellij-session", "physical-a", root,
	}, &stdout, &stderr, deps)
	if code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	if notifier.closeCalls != 1 {
		t.Fatalf("notifier Close calls=%d, want 1", notifier.closeCalls)
	}
}

func initializedTicketManagerProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ticketworker.InitializeProject(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	return root
}

type fakeRunner struct{ ran bool }

func (f *fakeRunner) Run(context.Context) error { f.ran = true; return nil }

type fakeVoiceNotifier struct {
	closeCalls int
}

func (*fakeVoiceNotifier) Notify(string) error { return nil }
func (f *fakeVoiceNotifier) Close() error {
	f.closeCalls++
	return nil
}

type fakeRoleClient struct{}

func (*fakeRoleClient) CreatePane(context.Context, transport.CreatePaneRequest) (transport.CreatePaneResponse, error) {
	return transport.CreatePaneResponse{}, nil
}
func (*fakeRoleClient) SendInput(context.Context, string, transport.SendInputRequest) error {
	return nil
}
func (*fakeRoleClient) SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	return transport.SnapshotOutputResponse{}, nil
}
func (*fakeRoleClient) ClosePane(context.Context, string) (transport.ClosePaneResponse, error) {
	return transport.ClosePaneResponse{}, nil
}
func (*fakeRoleClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	return transport.InspectRuntimeResponse{}, nil
}
func (*fakeRoleClient) StreamEvents(context.Context) (*transport.EventStream, error) { return nil, nil }
