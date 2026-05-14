package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func TestIntegrationCreateSnapshotAndClosePane(t *testing.T) {
	if os.Getenv("AGENTD_ZELLIJ_INTEGRATION") != "1" {
		t.Skip("set AGENTD_ZELLIJ_INTEGRATION=1 to create a real Zellij pane")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	service := NewService(Options{
		Registry: registry.New(),
		Backend: zellij.NewBackend(zellij.Options{
			Session: os.Getenv("ZELLIJ_SESSION_NAME"),
		}),
		NewPaneID: func() PaneID {
			return "integration-pane"
		},
	})

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
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		if _, err := service.ClosePane(closeCtx, ClosePaneRequest{PaneID: created.Pane.ID}); err != nil {
			t.Logf("ClosePane() cleanup error = %v", err)
		}
	}()

	var snapshot SnapshotOutputResponse
	for i := 0; i < 20; i++ {
		snapshot, err = service.SnapshotOutput(ctx, SnapshotOutputRequest{
			PaneID: created.Pane.ID,
			Full:   true,
		})
		if err != nil {
			t.Fatalf("SnapshotOutput() error = %v", err)
		}
		if strings.Contains(snapshot.Output, "agentd-runtime-smoke") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("SnapshotOutput() = %q, want agentd-runtime-smoke", snapshot.Output)
}

func formatTabID(id *ZellijTabID) string {
	if id == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *id)
}
