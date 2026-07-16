package chrome

import (
	"reflect"
	"testing"
	"time"
)

func TestBuildPlanCreatesChromeTabNetworkSupervisorPaneByDefault(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 34, 56, 123456789, time.UTC)

	payload, err := BuildPlan(PlanRequest{
		CWD:           "/repo",
		ZellijSession: "physical-a",
		SocketPath:    "/tmp/agentd.sock",
		Session:       "chrome-debug",
		RoleCommand:   []string{"/tmp/bin/zellij-agent", "role"},
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	if payload.Session != "chrome-debug" || payload.Layout != "single-tab" {
		t.Fatalf("payload session/layout = %q/%q, want chrome-debug/single-tab", payload.Session, payload.Layout)
	}
	if payload.ZellijSession != "physical-a" {
		t.Fatalf("payload.ZellijSession = %q, want physical-a", payload.ZellijSession)
	}
	if len(payload.Tabs) != 1 {
		t.Fatalf("len(Tabs) = %d, want 1", len(payload.Tabs))
	}
	tab := payload.Tabs[0]
	if tab.Name != "chrome" {
		t.Fatalf("tab.Name = %q, want chrome", tab.Name)
	}
	if len(tab.Panes) != 1 {
		t.Fatalf("len(Panes) = %d, want 1", len(tab.Panes))
	}
	pane := tab.Panes[0]
	if pane.ID != "chrome-tab-network-20260708-123456-123456789" {
		t.Fatalf("pane.ID = %q, want timestamped chrome tab-network id", pane.ID)
	}
	if pane.Role != "tab-network" || pane.CWD != "/repo" {
		t.Fatalf("pane role/cwd = %q/%q, want tab-network /repo", pane.Role, pane.CWD)
	}
	wantCommand := []string{
		"/tmp/bin/zellij-agent", "role", "tab-network",
		"--socket", "/tmp/agentd.sock",
		"--session", "chrome-debug",
		"--role-bin", "/tmp/bin/zellij-agent",
		"--spawn-on-new-tab",
		"--zellij-session", "physical-a",
	}
	if !reflect.DeepEqual(pane.Command, wantCommand) {
		t.Fatalf("pane.Command = %#v, want %#v", pane.Command, wantCommand)
	}
}

func TestBuildPlanNoWatchCreatesChromeTabNetworkPane(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 34, 56, 123456789, time.UTC)

	payload, err := BuildPlan(PlanRequest{
		CWD:            "/repo",
		ZellijSession:  "physical-a",
		Session:        "chrome-debug",
		RoleCommand:    []string{"zellij-agent", "role"},
		TabNetworkArgs: []string{"--port", "9333", "--no-launch"},
		NoWatch:        true,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	if payload.Session != "chrome-debug" {
		t.Fatalf("payload.Session = %q, want chrome-debug", payload.Session)
	}
	pane := payload.Tabs[0].Panes[0]
	if pane.ID != "chrome-tab-network-20260708-123456-123456789" || pane.Role != "tab-network" {
		t.Fatalf("pane = %#v, want timestamped tab-network pane", pane)
	}
	got := pane.Command
	want := []string{"zellij-agent", "role", "tab-network", "--port", "9333", "--no-launch"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
}

func TestBuildPlanPassesSupervisorChromeArgs(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 34, 56, 123456789, time.UTC)

	payload, err := BuildPlan(PlanRequest{
		CWD:            "/repo",
		ZellijSession:  "physical-a",
		SocketPath:     "/tmp/agentd.sock",
		Session:        "chrome-debug",
		RoleCommand:    []string{"zellij-agent", "role"},
		TabNetworkArgs: []string{"--port", "9333", "--no-launch"},
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	got := payload.Tabs[0].Panes[0].Command
	want := []string{
		"zellij-agent", "role", "tab-network",
		"--socket", "/tmp/agentd.sock",
		"--session", "chrome-debug",
		"--role-bin", "zellij-agent",
		"--spawn-on-new-tab",
		"--zellij-session", "physical-a",
		"--port", "9333",
		"--no-launch",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
}

func TestBuildPlanRejectsMissingCWD(t *testing.T) {
	_, err := BuildPlan(PlanRequest{})
	if err == nil {
		t.Fatal("BuildPlan() error = nil, want missing cwd error")
	}
}

func TestRequestIDUsesSession(t *testing.T) {
	if got := RequestID("chrome-debug"); got != "req_chrome-debug" {
		t.Fatalf("RequestID() = %q, want req_chrome-debug", got)
	}
}
