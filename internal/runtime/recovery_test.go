package runtime

import (
	"context"
	"errors"
	"testing"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

type recoveryBackend struct {
	*fakeBackend
	active    []string
	activeErr error
}

func (b *recoveryBackend) ActiveSessions(context.Context) ([]string, error) {
	return b.active, b.activeErr
}

func TestRecoveryChecksLiveIdentityAndKeepsUnmanagedPanes(t *testing.T) {
	reg := registry.New()
	tab := registry.ZellijTabID(1)
	for _, id := range []registry.PaneID{"live", "missing", "reused", "exited", "no-token"} {
		token := registry.OwnershipToken("owner")
		if id == "no-token" {
			token = ""
		}
		if _, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: id, SessionID: "s", TabID: "1", ZellijTabID: &tab, ZellijPaneID: registry.ZellijPaneID(id), OwnershipToken: token, Status: registry.PaneStatusRunning}); err != nil {
			t.Fatal(err)
		}
	}
	backend := &recoveryBackend{fakeBackend: &fakeBackend{listPanes: []zellij.Pane{{ID: "live", TabID: 1}, {ID: "reused", TabID: 2}, {ID: "exited", TabID: 1, Exited: true}, {ID: "no-token", TabID: 1}, {ID: "unmanaged", TabID: 1}}}, active: []string{"s"}}
	observer := &recordingPaneObserver{}
	service := NewService(Options{Registry: reg, Backend: backend, PaneObserver: observer})
	if err := service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertPaneStatus(t, service, "live", PaneStatusRunning)
	for _, id := range []PaneID{"missing", "reused", "no-token"} {
		assertPaneStatus(t, service, id, PaneStatusLost)
	}
	assertPaneMissing(t, service, "exited")
	if len(backend.closeRequests) != 0 {
		t.Fatalf("recovery closed live panes: %+v", backend.closeRequests)
	}
	if _, err := reg.GetLatestByZellijPaneID("unmanaged"); err == nil {
		t.Fatal("claimed unmanaged pane")
	}
}
func TestRecoveryInspectionFailurePreservesRegistry(t *testing.T) {
	reg := registry.New()
	before, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: "p", SessionID: "s", ZellijPaneID: "1", OwnershipToken: "owner", Status: registry.PaneStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	backend := &recoveryBackend{fakeBackend: &fakeBackend{}, activeErr: errors.New("offline")}
	service := NewService(Options{Registry: reg, Backend: backend})
	if err = service.Recover(context.Background()); err == nil {
		t.Fatal("expected inspection failure")
	}
	after, err := reg.GetPane("p")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != before.Status || after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("failed inspection changed registry: %+v", after)
	}
}
func TestRecoveryMissingSessionDoesNotQueryOrCloseIt(t *testing.T) {
	reg := registry.New()
	reg.RegisterPane(registry.RegisterPaneRequest{ID: "p", SessionID: "gone", ZellijPaneID: "1", OwnershipToken: "owner", Status: registry.PaneStatusRunning})
	backend := &recoveryBackend{fakeBackend: &fakeBackend{}}
	service := NewService(Options{Registry: reg, Backend: backend})
	if err := service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertPaneStatus(t, service, "p", PaneStatusLost)
}
