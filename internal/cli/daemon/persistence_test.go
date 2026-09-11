package daemoncli

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/persistence"
	agentruntime "zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/zellij"
)

func TestDaemonBundleRestartsWithAgentSettingsAndFreshMonitoring(t *testing.T) {
	restoreDaemonFactories(t)
	backend := newDaemonFakeBackend()
	newDaemonBackend = func() daemonBackend { return backend }
	newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner { return daemonFakeSubscriptionRunner{} }
	path := filepath.Join(t.TempDir(), "daemon.db")
	writer, err := persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := newRuntimeBundle(writer)
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.service.StartAgent(context.Background(), codingagent.StartAgentRequest{Kind: codingagent.KindClaude, CWD: t.TempDir(), SourceZellijSession: "s", SourceZellijPaneID: "source-pane"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := first.service.StartAgent(context.Background(), codingagent.StartAgentRequest{
		Kind: codingagent.KindClaude, CWD: t.TempDir(), SourceZellijSession: "s",
		SourceZellijPaneID: "source-pane", ParentPaneID: created.Agent.Pane.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Agent.Agent.ID
	if _, err = first.store.SetPinned(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err = first.store.SetTaskAlias(id, codingagent.TaskAliasReview); err != nil {
		t.Fatal(err)
	}
	first.stop()
	// Simulate the last durable observation being working.
	if _, err = first.store.UpdateState(id, codingagent.StateUpdate{State: codingagent.StateWorking}); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 2)
	newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner { return recoverySubscriptionRunner{started: started} }
	writer, err = persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newRuntimeBundle(writer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		second.stop()
		if err := writer.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if err = second.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("restored pane subscription not started")
	}
	got, err := second.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Pinned || got.TaskAlias != codingagent.TaskAliasReview || got.State != codingagent.StateUnknown {
		t.Fatalf("restored agent=%+v", got)
	}
	pane, err := second.service.InspectPane(context.Background(), agentruntime.InspectPaneRequest{PaneID: got.PaneID})
	if err != nil {
		t.Fatal(err)
	}
	if pane.Pane.OwnershipToken != created.Agent.Pane.OwnershipToken {
		t.Fatal("ownership token changed on restart")
	}
	restoredChild, err := second.service.InspectPane(context.Background(), agentruntime.InspectPaneRequest{PaneID: child.Agent.Pane.ID})
	if err != nil || restoredChild.Pane.ParentPaneID != created.Agent.Pane.ID {
		t.Fatalf("restored child=%+v error=%v", restoredChild, err)
	}
	if _, err := second.store.Get(child.Agent.Agent.ID); err != nil {
		t.Fatal(err)
	}
	backend.addPane(zellij.Pane{ID: "second-pane", TabID: 7})
	next, err := second.service.StartAgent(context.Background(), codingagent.StartAgentRequest{Kind: codingagent.KindClaude, CWD: t.TempDir(), SourceZellijSession: "s", SourceZellijPaneID: "second-pane"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Agent.Agent.ID == id {
		t.Fatal("agent ID reused after restart")
	}
}

func TestParseServeDBAndExclusiveDaemon(t *testing.T) {
	socket, path, ok := parseServeArgs([]string{"--db", "custom.db", "--socket", "custom.sock"}, io.Discard)
	if !ok || socket != "custom.sock" || path != "custom.db" {
		t.Fatal(socket, path, ok)
	}
	path = filepath.Join(t.TempDir(), "daemon.db")
	writer, err := persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if code := RunContext(ctx, []string{"serve", "--db", path, "--socket", filepath.Join(t.TempDir(), "s.sock")}, io.Discard, io.Discard); code != 1 {
		t.Fatalf("second daemon exit=%d", code)
	}
}

type recoverySubscriptionRunner struct{ started chan struct{} }

func (r recoverySubscriptionRunner) Start(ctx context.Context, spec zellij.CommandSpec) (*agentruntime.SubscriptionStream, error) {
	r.started <- struct{}{}
	return (daemonFakeSubscriptionRunner{}).Start(ctx, spec)
}
