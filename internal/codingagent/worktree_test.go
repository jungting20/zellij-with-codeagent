package codingagent

import (
	"context"
	"errors"
	"testing"
	"zellij-with-codeagent/internal/runtime"
)

type childRuntime struct {
	*serviceFakeRuntime
	request runtime.CreatePaneRequest
	err     error
}

func (f *childRuntime) CreatePane(_ context.Context, req runtime.CreatePaneRequest) (runtime.CreatePaneResponse, error) {
	f.request = req
	return runtime.CreatePaneResponse{Pane: runtime.Pane{ID: req.ID, ParentPaneID: req.ParentPaneID}}, f.err
}
func TestStartChildCreatesPaneAndRollsBackFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		store := NewMemoryStore(nil)
		rt := &childRuntime{serviceFakeRuntime: &serviceFakeRuntime{}}
		if fail {
			rt.err = errors.New("create failed")
		}
		service := NewService(ServiceOptions{RuntimeService: rt, Store: store, LifecycleMonitor: &serviceFakeMonitor{}, NewAgentID: func() ID { return "child" }})
		cwd := t.TempDir()
		response, err := service.StartAgent(context.Background(), StartAgentRequest{Kind: KindCodex, CWD: cwd, ParentPaneID: "parent", SourceZellijSession: "session", SourceZellijPaneID: "1"})
		if (err != nil) != fail {
			t.Fatalf("error=%v", err)
		}
		if len(rt.claimed) != 0 || rt.request.ParentPaneID != "parent" || rt.request.SameTabAsPaneID != "parent" || rt.request.CWD != cwd || len(rt.request.Command) == 0 {
			t.Fatalf("request=%+v", rt.request)
		}
		if fail {
			if _, err := store.Get("child"); !errors.Is(err, ErrNotFound) {
				t.Fatal("failed child record retained")
			}
		} else if response.Agent.Pane.ParentPaneID != "parent" {
			t.Fatal("missing parent")
		}
	}
}

func TestStartChildInTargetSession(t *testing.T) {
	for _, fail := range []bool{false, true} {
		store := NewMemoryStore(nil)
		rt := &childRuntime{serviceFakeRuntime: &serviceFakeRuntime{}}
		if fail {
			rt.err = errors.New("session creation failed")
		}
		service := NewService(ServiceOptions{RuntimeService: rt, Store: store, LifecycleMonitor: &serviceFakeMonitor{}, NewAgentID: func() ID { return "child" }})
		_, err := service.StartAgent(context.Background(), StartAgentRequest{
			Kind: KindCodex, CWD: t.TempDir(), ParentPaneID: "parent",
			SourceZellijSession: "source", SourceZellijPaneID: "1", TargetSession: "worktree-agent",
		})
		if (err != nil) != fail {
			t.Fatalf("error = %v", err)
		}
		if len(rt.claimed) != 0 || rt.request.ZellijSession != "worktree-agent" || !rt.request.NewTab || !rt.request.EnsureSession || rt.request.SameTabAsPaneID != "" || rt.request.ParentPaneID != "parent" {
			t.Fatalf("request = %+v", rt.request)
		}
		if fail {
			if _, err := store.Get("child"); !errors.Is(err, ErrNotFound) {
				t.Fatal("failed child record retained")
			}
		}
	}
}
