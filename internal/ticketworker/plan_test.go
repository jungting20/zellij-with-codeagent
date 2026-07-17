package ticketworker

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildStartPlanCreatesTicketManagerAnchor(t *testing.T) {
	root := t.TempDir()
	got, err := BuildStartPlan(StartPlanRequest{
		Root:          root,
		ZellijSession: "physical-a",
		SocketPath:    "/tmp/tickets.sock",
		Executable:    []string{"/opt/bin/zellij-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Layout != "single-tab" || len(got.Tabs) != 1 || len(got.Tabs[0].Panes) != 1 {
		t.Fatalf("plan shape = %#v", got)
	}
	pane := got.Tabs[0].Panes[0]
	if got.Tabs[0].Name != "ticket-worker" || pane.Role != "ticket-manager" || pane.CWD != root {
		t.Fatalf("manager pane = %#v", pane)
	}
	if !strings.HasPrefix(got.Session, "ticket-worker-") || !strings.HasPrefix(pane.ID, "ticket-manager-") {
		t.Fatalf("identities = session %q pane %q", got.Session, pane.ID)
	}
	if got.ZellijSession != "physical-a" {
		t.Fatalf("ZellijSession = %q, want physical-a", got.ZellijSession)
	}
	wantCommand := []string{
		"/opt/bin/zellij-agent", "role", "ticket-manager",
		"--socket", "/tmp/tickets.sock",
		"--task", got.Session,
		"--anchor-pane", pane.ID,
		"--zellij-session", "physical-a",
		root,
	}
	if !reflect.DeepEqual(pane.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", pane.Command, wantCommand)
	}
	if gotRequestID := StartRequestID(got.Session); gotRequestID != "req_"+got.Session {
		t.Fatalf("StartRequestID() = %q", gotRequestID)
	}
}

func TestBuildStartPlanIdentityIsStableAndProjectScoped(t *testing.T) {
	request := func(root string) StartPlanRequest {
		return StartPlanRequest{Root: root, ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za"}}
	}
	first, err := BuildStartPlan(request("/repo/a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := BuildStartPlan(request("/repo/a"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := BuildStartPlan(request("/repo/b"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Session != again.Session || first.Tabs[0].Panes[0].ID != again.Tabs[0].Panes[0].ID {
		t.Fatal("same root produced unstable identities")
	}
	if first.Session == other.Session || first.Tabs[0].Panes[0].ID == other.Tabs[0].Panes[0].ID {
		t.Fatal("different roots collided")
	}
}

func TestBuildStartPlanDoesNotMutateExecutable(t *testing.T) {
	executable := make([]string, 1, 16)
	executable[0] = " zellij-agent "
	got, err := BuildStartPlan(StartPlanRequest{
		Root: "/repo", ZellijSession: "z", SocketPath: "/tmp/a", Executable: executable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if executable[0] != " zellij-agent " || len(executable) != 1 {
		t.Fatalf("Executable mutated: %#v", executable)
	}
	if got.Tabs[0].Panes[0].Command[0] != "zellij-agent" {
		t.Fatalf("normalized command = %#v", got.Tabs[0].Panes[0].Command)
	}
}

func TestBuildStartPlanRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		req  StartPlanRequest
	}{
		{name: "empty root", req: StartPlanRequest{ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za"}}},
		{name: "relative root", req: StartPlanRequest{Root: "repo", ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za"}}},
		{name: "empty session", req: StartPlanRequest{Root: "/repo", SocketPath: "/tmp/a", Executable: []string{"za"}}},
		{name: "empty socket", req: StartPlanRequest{Root: "/repo", ZellijSession: "z", Executable: []string{"za"}}},
		{name: "empty executable", req: StartPlanRequest{Root: "/repo", ZellijSession: "z", SocketPath: "/tmp/a"}},
		{name: "blank executable argument", req: StartPlanRequest{Root: "/repo", ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za", " "}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildStartPlan(tt.req); err == nil {
				t.Fatal("BuildStartPlan() error = nil")
			}
		})
	}
}
