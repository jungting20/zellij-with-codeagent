package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func TestIntegrationCreateSnapshotAndClosePane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	service, _ := newIntegrationService(t, "integration-pane")

	created, err := service.CreatePane(ctx, CreatePaneRequest{
		Role:    PaneRoleTest,
		NewTab:  true,
		TabName: "agentd-smoke",
		Command: []string{"sh", "-lc", "printf 'agentd-runtime-smoke\n'; sleep 30"},
		CWD:     ".",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	t.Logf(
		"created runtime pane %s backed by zellij pane %s in tab %s (%s)",
		created.Pane.ID,
		created.Pane.ZellijPaneID,
		formatTabID(created.Pane.ZellijTabID),
		created.Pane.TabName,
	)

	defer func() {
		closeIntegrationPane(t, service, created.Pane.ID)
	}()

	waitForSnapshotContains(ctx, t, service, created.Pane.ID, "agentd-runtime-smoke")
}

func TestIntegrationCreateNewTabSendInputAndClosePane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	service, _ := newIntegrationService(t, "integration-input-pane")

	created, err := service.CreatePane(ctx, CreatePaneRequest{
		Role:    PaneRoleCoder,
		NewTab:  true,
		TabName: "agentd-input",
		Command: []string{"sh"},
		CWD:     ".",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	defer closeIntegrationPane(t, service, created.Pane.ID)

	if created.Pane.ZellijTabID == nil || created.Pane.TabName == "" {
		t.Fatalf("CreatePane() pane = %#v, want tab metadata", created.Pane)
	}

	if err := service.SendInput(ctx, SendInputRequest{
		PaneID: created.Pane.ID,
		Text:   "printf 'agentd-runtime-input-ok\\n'\n",
	}); err != nil {
		t.Fatalf("SendInput() error = %v", err)
	}

	waitForSnapshotContains(ctx, t, service, created.Pane.ID, "agentd-runtime-input-ok")
}

func TestIntegrationCreatePaneInExistingTabAndClosePane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	service, _ := newIntegrationService(t, "integration-existing-tab")

	base, err := service.CreatePane(ctx, CreatePaneRequest{
		ID:      "integration-existing-tab-base",
		Role:    PaneRoleTest,
		NewTab:  true,
		TabName: "agentd-existing-tab",
		Command: []string{"sh", "-lc", "printf 'agentd-existing-tab-base\n'; sleep 30"},
		CWD:     ".",
	})
	if err != nil {
		t.Fatalf("CreatePane() base error = %v", err)
	}
	defer closeIntegrationPane(t, service, base.Pane.ID)

	if base.Pane.ZellijTabID == nil {
		t.Fatalf("CreatePane() base tab ID = nil, want created tab ID")
	}
	tabID := *base.Pane.ZellijTabID

	target, err := service.CreatePane(ctx, CreatePaneRequest{
		ID:          "integration-existing-tab-target",
		Role:        PaneRoleTest,
		ZellijTabID: &tabID,
		Command:     []string{"sh", "-lc", "printf 'agentd-existing-tab-target\n'; sleep 30"},
		CWD:         ".",
	})
	if err != nil {
		t.Fatalf("CreatePane() target error = %v", err)
	}
	defer closeIntegrationPane(t, service, target.Pane.ID)

	if target.Pane.ZellijTabID == nil || *target.Pane.ZellijTabID != tabID {
		t.Fatalf("CreatePane() target tab ID = %v, want %d", target.Pane.ZellijTabID, tabID)
	}

	waitForSnapshotContains(ctx, t, service, target.Pane.ID, "agentd-existing-tab-target")
}

func TestIntegrationCreatePaneWithoutRuntimeClosePane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	service, backend := newIntegrationService(t, "integration-open-pane")

	created, err := service.CreatePane(ctx, CreatePaneRequest{
		Role:    PaneRoleLog,
		NewTab:  true,
		TabName: "agentd-left-open",
		Command: []string{"sh", "-lc", "printf 'agentd-left-open\n'; sleep 30"},
		CWD:     ".",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if created.Pane.ZellijTabID == nil {
		t.Fatalf("CreatePane() tab ID = nil, want cleanup tab ID")
	}

	// This scenario intentionally does not call RuntimeService.ClosePane. The
	// test cleanup closes the created tab directly so the integration run does
	// not leave a long-lived Zellij pane behind.
	tabID := zellij.TabID(*created.Pane.ZellijTabID)
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		if err := backend.CloseTab(closeCtx, zellij.CloseTabRequest{TabID: &tabID}); err != nil {
			t.Logf("direct CloseTab cleanup error = %v", err)
		}
	})

	waitForSnapshotContains(ctx, t, service, created.Pane.ID, "agentd-left-open")

	inspected, err := service.InspectPane(ctx, InspectPaneRequest{PaneID: created.Pane.ID})
	if err != nil {
		t.Fatalf("InspectPane() error = %v", err)
	}
	if inspected.Pane.Status == PaneStatusClosed {
		t.Fatalf("InspectPane() status = %q, want not closed without RuntimeService.ClosePane", inspected.Pane.Status)
	}

	list, err := service.ListPanes(ctx)
	if err != nil {
		t.Fatalf("ListPanes() error = %v", err)
	}
	if len(list.Panes) != 1 || list.Panes[0].ID != created.Pane.ID {
		t.Fatalf("ListPanes() = %#v, want open runtime pane", list.Panes)
	}
}

func TestE2ECreateTabAndFourPanesPrintRegistry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service, _ := newE2EService(t)
	panes := make([]Pane, 0, 4)
	tabName := "agentd-e2e-four-panes"

	first, err := service.CreatePane(ctx, CreatePaneRequest{
		ID:      "e2e-pane-1",
		Role:    PaneRoleCoder,
		Name:    "e2e-pane-1",
		NewTab:  true,
		TabName: tabName,
		Command: e2ePaneCommand("agentd-e2e-pane-1"),
		CWD:     ".",
	})
	if err != nil {
		t.Fatalf("CreatePane() first error = %v", err)
	}
	if first.Pane.ZellijTabID == nil {
		t.Fatalf("CreatePane() first tab ID = nil, want created tab ID")
	}
	tabID := *first.Pane.ZellijTabID
	panes = append(panes, first.Pane)

	requests := []struct {
		id     PaneID
		role   PaneRole
		marker string
	}{
		{id: "e2e-pane-2", role: PaneRoleTest, marker: "agentd-e2e-pane-2"},
		{id: "e2e-pane-3", role: PaneRoleBuild, marker: "agentd-e2e-pane-3"},
		{id: "e2e-pane-4", role: PaneRoleLog, marker: "agentd-e2e-pane-4"},
	}
	for _, req := range requests {
		created, err := service.CreatePane(ctx, CreatePaneRequest{
			ID:          req.id,
			Role:        req.role,
			Name:        string(req.id),
			ZellijTabID: &tabID,
			Command:     e2ePaneCommand(req.marker),
			CWD:         ".",
		})
		if err != nil {
			t.Fatalf("CreatePane() %s error = %v", req.id, err)
		}
		panes = append(panes, created.Pane)
	}

	for i, pane := range panes {
		marker := fmt.Sprintf("agentd-e2e-pane-%d", i+1)
		waitForSnapshotContains(ctx, t, service, pane.ID, marker)
		if pane.ZellijTabID == nil || *pane.ZellijTabID != tabID {
			t.Fatalf("created pane %s tab ID = %v, want %d", pane.ID, pane.ZellijTabID, tabID)
		}
		t.Logf("created %s -> zellij pane %s in tab %d", pane.ID, pane.ZellijPaneID, tabID)
	}

	list, err := service.ListPanes(ctx)
	if err != nil {
		t.Fatalf("ListPanes() error = %v", err)
	}
	if len(list.Panes) != 4 {
		t.Fatalf("ListPanes() returned %d panes, want 4: %#v", len(list.Panes), list.Panes)
	}

	expected := map[PaneID]bool{
		"e2e-pane-1": false,
		"e2e-pane-2": false,
		"e2e-pane-3": false,
		"e2e-pane-4": false,
	}
	for _, pane := range list.Panes {
		if _, ok := expected[pane.ID]; !ok {
			t.Fatalf("ListPanes() included unexpected pane %s: %#v", pane.ID, list.Panes)
		}
		if pane.ZellijTabID == nil || *pane.ZellijTabID != tabID {
			t.Fatalf("registry pane %s tab ID = %v, want %d", pane.ID, pane.ZellijTabID, tabID)
		}
		if pane.Status == PaneStatusClosed {
			t.Fatalf("registry pane %s status = %q, want not closed", pane.ID, pane.Status)
		}
		expected[pane.ID] = true
	}
	for id, seen := range expected {
		if !seen {
			t.Fatalf("ListPanes() missing %s: %#v", id, list.Panes)
		}
	}

	registryJSON, err := json.MarshalIndent(list.Panes, "", "  ")
	if err != nil {
		t.Fatalf("marshal registry panes: %v", err)
	}
	t.Logf("runtime registry after creating tab %d (%s) and 4 panes:\n%s", tabID, tabName, registryJSON)
}

func formatTabID(id *ZellijTabID) string {
	if id == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *id)
}

func newIntegrationService(t *testing.T, paneID PaneID) (*Service, *zellij.CLIBackend) {
	t.Helper()
	if os.Getenv("AGENTD_ZELLIJ_INTEGRATION") != "1" {
		t.Skip("set AGENTD_ZELLIJ_INTEGRATION=1 to create real Zellij panes")
	}

	backend := zellij.NewBackend(zellij.Options{
		Session: os.Getenv("ZELLIJ_SESSION_NAME"),
	})
	service := NewService(Options{
		Registry: registry.New(),
		Backend:  backend,
		NewPaneID: func() PaneID {
			return paneID
		},
	})
	return service, backend
}

func newE2EService(t *testing.T) (*Service, *zellij.CLIBackend) {
	t.Helper()
	if os.Getenv("AGENTD_ZELLIJ_E2E") != "1" {
		t.Skip("set AGENTD_ZELLIJ_E2E=1 to create real Zellij panes that are intentionally left open")
	}

	backend := zellij.NewBackend(zellij.Options{
		Session: os.Getenv("ZELLIJ_SESSION_NAME"),
	})
	service := NewService(Options{
		Registry: registry.New(),
		Backend:  backend,
	})
	return service, backend
}

func e2ePaneCommand(marker string) []string {
	return []string{"sh", "-lc", fmt.Sprintf("printf '%s\n'; sleep 600", marker)}
}

func closeIntegrationPane(t *testing.T, service *Service, paneID PaneID) {
	t.Helper()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer closeCancel()
	if _, err := service.ClosePane(closeCtx, ClosePaneRequest{PaneID: paneID}); err != nil {
		t.Logf("ClosePane() cleanup error = %v", err)
	}
}

func waitForSnapshotContains(ctx context.Context, t *testing.T, service *Service, paneID PaneID, marker string) SnapshotOutputResponse {
	t.Helper()

	var snapshot SnapshotOutputResponse
	var err error
	for i := 0; i < 20; i++ {
		snapshot, err = service.SnapshotOutput(ctx, SnapshotOutputRequest{
			PaneID: paneID,
			Full:   true,
		})
		if err != nil {
			t.Fatalf("SnapshotOutput() error = %v", err)
		}
		if strings.Contains(snapshot.Output, marker) {
			return snapshot
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("SnapshotOutput() = %q, want %s", snapshot.Output, marker)
	return SnapshotOutputResponse{}
}
