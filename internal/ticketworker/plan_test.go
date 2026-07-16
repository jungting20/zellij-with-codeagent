package ticketworker

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"
)

func TestBuildPlanCreatesManagerAndMonitorInOneTicketWorkerTab(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		CWD:        "/repo",
		ConfigPath: "/repo/.zellij-agent/worker/config.yaml",
		Session:    "ticket-session",
		Executable: []string{"/tmp/bin/zellij-agent"},
		Config:     Config{MaxWorkers: 3},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	if payload.Session != "ticket-session" || payload.Layout != "triple-horizontal" {
		t.Fatalf("session/layout = %q/%q, want ticket-session/triple-horizontal", payload.Session, payload.Layout)
	}
	if len(payload.Tabs) != 1 || payload.Tabs[0].Name != "ticket-worker" {
		t.Fatalf("tabs = %#v, want one ticket-worker tab", payload.Tabs)
	}
	panes := payload.Tabs[0].Panes
	if len(panes) != 2 {
		t.Fatalf("len(panes) = %d, want 2", len(panes))
	}
	if panes[0].ID != "ticket-worker-manager" || panes[1].ID != "ticket-worker-monitor" {
		t.Fatalf("pane order = %q, %q, want manager then monitor", panes[0].ID, panes[1].ID)
	}
	if panes[0].CWD != "/repo" || panes[1].CWD != "/repo" {
		t.Fatalf("pane cwd = %q, %q, want /repo", panes[0].CWD, panes[1].CWD)
	}
	wantManager := []string{
		"/tmp/bin/zellij-agent", "ticket-worker", "manager",
		"--cwd", "/repo",
		"--config", "/repo/.zellij-agent/worker/config.yaml",
		"--task", "ticket-session",
		"--anchor", "ticket-worker-manager",
	}
	if !reflect.DeepEqual(panes[0].Command, wantManager) {
		t.Fatalf("manager command = %#v, want %#v", panes[0].Command, wantManager)
	}
	wantMonitor := []string{
		"/tmp/bin/zellij-agent", "dashboard",
		"--task", "ticket-session",
		"--read-only",
		"--capacity", "3",
	}
	if !reflect.DeepEqual(panes[1].Command, wantMonitor) {
		t.Fatalf("monitor command = %#v, want %#v", panes[1].Command, wantMonitor)
	}
}

func TestSessionIDIncludesUTCTimestampAndCWDHash(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 34, 56, 0, time.FixedZone("KST", 9*60*60))
	hash := sha256.Sum256([]byte("/repo"))
	want := fmt.Sprintf("ticket-worker-20260716-033456-%x", hash[:4])
	if got := SessionID("/repo", now); got != want {
		t.Fatalf("SessionID() = %q, want %q", got, want)
	}
}

func TestBuildPlanDerivesCollisionResistantSession(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		CWD:        "/repo",
		ConfigPath: "/repo/config.yaml",
		Executable: []string{"zellij-agent"},
		Config: Config{
			Version:      1,
			MaxWorkers:   1,
			PollInterval: time.Second,
			Worker:       WorkerConfig{Command: []string{"worker"}, CompletionMarker: "DONE"},
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if !regexp.MustCompile(`^ticket-worker-[0-9]{8}-[0-9]{6}-[0-9a-f]{8}$`).MatchString(payload.Session) {
		t.Fatalf("session = %q, want timestamp and cwd hash", payload.Session)
	}
}

func TestBuildPlanRejectsInvalidRequestBeforeBuildingCommands(t *testing.T) {
	tests := []struct {
		name string
		req  PlanRequest
	}{
		{name: "missing cwd", req: PlanRequest{ConfigPath: "/repo/config.yaml", Executable: []string{"zellij-agent"}}},
		{name: "missing config", req: PlanRequest{CWD: "/repo", Executable: []string{"zellij-agent"}}},
		{name: "missing executable", req: PlanRequest{CWD: "/repo", ConfigPath: "/repo/config.yaml"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildPlan(tt.req); err == nil {
				t.Fatal("BuildPlan() error = nil, want invalid request error")
			}
		})
	}
}
