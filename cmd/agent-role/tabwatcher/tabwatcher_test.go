package tabwatcher

import (
	"reflect"
	"testing"
	"time"
)

func TestParseOptionsDefaults(t *testing.T) {
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions(nil) error = %v", err)
	}
	if opts.Port != 9222 {
		t.Fatalf("Port = %d, want 9222", opts.Port)
	}
	if opts.SocketPath != "/tmp/agentd.sock" {
		t.Fatalf("SocketPath = %q, want /tmp/agentd.sock", opts.SocketPath)
	}
	if opts.Session != "chrome-tabs" {
		t.Fatalf("Session = %q, want chrome-tabs", opts.Session)
	}
	if opts.RoleBin != "zellij-agent" {
		t.Fatalf("RoleBin = %q, want zellij-agent", opts.RoleBin)
	}
	if opts.UserDataDir != defaultUserDataDir {
		t.Fatalf("UserDataDir = %q, want %q", opts.UserDataDir, defaultUserDataDir)
	}
	if !opts.LaunchChrome {
		t.Fatal("LaunchChrome = false, want true")
	}
	if opts.PollInterval != 500*time.Millisecond {
		t.Fatalf("PollInterval = %s, want 500ms", opts.PollInterval)
	}
}

func TestParseOptionsAcceptsCustomValues(t *testing.T) {
	opts, err := parseOptions([]string{
		"--port", "9333",
		"--socket", "/tmp/custom.sock",
		"--cwd", "/repo",
		"--session", "chrome-debug",
		"--role-bin", "/tmp/bin/zellij-agent",
		"--chrome-path", "/Applications/Chrome",
		"--user-data-dir", "/tmp/profile",
		"--no-launch",
		"--poll-interval", "250ms",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if opts.Port != 9333 || opts.SocketPath != "/tmp/custom.sock" || opts.CWD != "/repo" || opts.Session != "chrome-debug" {
		t.Fatalf("options = %#v, want custom port/socket/cwd/session", opts)
	}
	if opts.RoleBin != "/tmp/bin/zellij-agent" || opts.ChromePath != "/Applications/Chrome" || opts.UserDataDir != "/tmp/profile" {
		t.Fatalf("options = %#v, want custom executable paths", opts)
	}
	if opts.LaunchChrome {
		t.Fatal("LaunchChrome = true, want false")
	}
	if opts.PollInterval != 250*time.Millisecond {
		t.Fatalf("PollInterval = %s, want 250ms", opts.PollInterval)
	}
}

func TestBuildTargetPlanCreatesTabNetworkPaneForTarget(t *testing.T) {
	cfg := watcherConfig{
		Port:       9333,
		CWD:        "/repo",
		Session:    "chrome-tabs",
		RoleBin:    "/tmp/bin/zellij-agent",
		SocketPath: "/tmp/agentd.sock",
	}

	requestID, payload := buildTargetPlan(cfg, PageTarget{ID: "ABCDEF1234567890", Type: "page", URL: "https://example.com"})

	if requestID != "req_chrome-tab-network-ABCDEF123456" {
		t.Fatalf("requestID = %q, want target-specific request id", requestID)
	}
	if payload.Session != "chrome-tabs" || payload.Layout != "single-tab" || len(payload.Tabs) != 1 {
		t.Fatalf("payload = %#v, want one-tab chrome-tabs plan", payload)
	}
	if payload.Tabs[0].Name != "chrome:ABCDEF123456" {
		t.Fatalf("tab name = %q, want chrome:ABCDEF123456", payload.Tabs[0].Name)
	}
	pane := payload.Tabs[0].Panes[0]
	if pane.ID != "chrome-tab-network-ABCDEF123456" || pane.Role != "tab-network" || pane.CWD != "/repo" {
		t.Fatalf("pane = %#v, want deterministic tab-network pane in cwd", pane)
	}
	wantCommand := []string{"/tmp/bin/zellij-agent", "role", "tab-network", "--port", "9333", "--no-launch", "--target-id", "ABCDEF1234567890"}
	if !reflect.DeepEqual(pane.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", pane.Command, wantCommand)
	}
}
