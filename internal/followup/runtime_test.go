package followup

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/runtime"
)

type adapterFixture struct {
	output                  string
	before, after           codingagent.AgentExplanation
	pane                    runtime.Pane
	snapshot                bool
	steps                   []string
	explainErr, afterErr    error
	inspectErr, snapshotErr error
	sendErr                 error
	inputs                  []runtime.SendInputRequest
	inspections, snapshots  []runtime.PaneID
	explanations            []codingagent.ID
}

func (f *adapterFixture) ExplainAgent(_ context.Context, id codingagent.ID) (codingagent.AgentExplanation, error) {
	f.steps = append(f.steps, "explain")
	f.explanations = append(f.explanations, id)
	if f.snapshot {
		return f.after, f.afterErr
	}
	return f.before, f.explainErr
}

func (f *adapterFixture) InspectPane(_ context.Context, req runtime.InspectPaneRequest) (runtime.InspectPaneResponse, error) {
	f.steps = append(f.steps, "inspect")
	f.inspections = append(f.inspections, req.PaneID)
	return runtime.InspectPaneResponse{Pane: f.pane}, f.inspectErr
}

func (f *adapterFixture) SnapshotOutput(_ context.Context, req runtime.SnapshotOutputRequest) (runtime.SnapshotOutputResponse, error) {
	f.steps = append(f.steps, "snapshot")
	f.snapshots = append(f.snapshots, req.PaneID)
	f.snapshot = true
	output := f.output
	if output == "" {
		output = "› "
		if f.after.Kind == codingagent.KindClaude {
			output = "╭────────╮\n│ ❯ \n╰────────╯"
		}
	}
	return runtime.SnapshotOutputResponse{Pane: f.pane, Output: output}, f.snapshotErr
}

func (f *adapterFixture) SendInput(_ context.Context, req runtime.SendInputRequest) error {
	f.steps = append(f.steps, "send")
	f.inputs = append(f.inputs, req)
	return f.sendErr
}

func readyExplanation() codingagent.AgentExplanation {
	return codingagent.AgentExplanation{
		AgentID: "agent", Kind: codingagent.KindCodex, PaneID: "pane", State: codingagent.StateIdle,
		ObservationAvailable: true, ObservationEpoch: 9, WorkingRevision: 12,
		ObservedAt: time.Unix(200, 0), LastWorkingAt: time.Unix(190, 0),
		Detection: &codingagent.DetectionExplanation{Detection: codingagent.Detection{
			State: codingagent.StateIdle, VisibleIdle: true, RuleID: "ready_prompt",
		}},
	}
}

func newAdapterFixture() *adapterFixture {
	return &adapterFixture{
		before: readyExplanation(), after: readyExplanation(),
		pane: runtime.Pane{ID: "pane", OwnershipToken: "owner-token", Status: runtime.PaneStatusRunning},
	}
}

func TestRuntimeAdapterUsesFreshSnapshotBeforeDecidingReady(t *testing.T) {
	for _, kind := range []codingagent.Kind{codingagent.KindCodex, codingagent.KindClaude} {
		t.Run(string(kind), func(t *testing.T) {
			f := newAdapterFixture()
			f.before.Kind, f.after.Kind = kind, kind
			f.before.State = codingagent.StateWorking
			f.before.WorkingRevision = 10
			f.before.ObservedAt = time.Unix(100, 0)
			observation, err := (RuntimeAdapter{Agents: f, Runtime: f}).Observe(context.Background(), "agent")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.steps, []string{"explain", "inspect", "snapshot", "explain"}) {
				t.Fatalf("call order = %v", f.steps)
			}
			if !observation.Ready || !observation.Running || observation.State != "idle" || observation.Epoch != 9 || observation.WorkingRevision != 12 || !observation.ObservedAt.Equal(f.after.ObservedAt) || !observation.LastWorkingAt.Equal(f.after.LastWorkingAt) {
				t.Fatalf("not a fresh ready observation: %+v", observation)
			}
			if observation.AgentID != "agent" || observation.PaneID != "pane" || observation.OwnershipToken != "owner-token" {
				t.Fatalf("lost managed target identity: %+v", observation)
			}
			if !reflect.DeepEqual(f.explanations, []codingagent.ID{"agent", "agent"}) || !reflect.DeepEqual(f.inspections, []runtime.PaneID{"pane"}) || !reflect.DeepEqual(f.snapshots, []runtime.PaneID{"pane"}) {
				t.Fatalf("incorrect targets: explanations=%v inspections=%v snapshots=%v", f.explanations, f.inspections, f.snapshots)
			}
		})
	}
}

func TestRuntimeAdapterRejectsUncertainReadinessAfterSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*codingagent.AgentExplanation)
	}{
		{"fresh working overrides stale idle", func(e *codingagent.AgentExplanation) { e.State = codingagent.StateWorking }},
		{"blocked", func(e *codingagent.AgentExplanation) { e.State = codingagent.StateBlocked }},
		{"unknown", func(e *codingagent.AgentExplanation) { e.State = codingagent.StateUnknown }},
		{"no observation", func(e *codingagent.AgentExplanation) { e.ObservationAvailable = false }},
		{"startup grace", func(e *codingagent.AgentExplanation) { e.StartupGrace = true }},
		{"pending idle", func(e *codingagent.AgentExplanation) { e.PendingIdle = true }},
		{"no detection", func(e *codingagent.AgentExplanation) { e.Detection = nil }},
		{"no visible prompt", func(e *codingagent.AgentExplanation) { e.Detection.Detection.VisibleIdle = false }},
		{"idle fallback", func(e *codingagent.AgentExplanation) { e.Detection.Detection.Fallback = true }},
		{"skip state update", func(e *codingagent.AgentExplanation) { e.Detection.Detection.SkipStateUpdate = true }},
		{"interrupted conversation", func(e *codingagent.AgentExplanation) { e.Detection.Detection.RuleID = "conversation_interrupted" }},
		{"detection blocked before monitor transition", func(e *codingagent.AgentExplanation) { e.Detection.Detection.State = codingagent.StateBlocked }},
		{"detection working before monitor transition", func(e *codingagent.AgentExplanation) { e.Detection.Detection.State = codingagent.StateWorking }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdapterFixture()
			tc.alter(&f.after)
			observation, err := (RuntimeAdapter{Agents: f, Runtime: f}).Observe(context.Background(), "agent")
			if err != nil || observation.Ready || !observation.Running {
				t.Fatalf("uncertain state accepted or running identity lost: %+v, %v", observation, err)
			}
			if !f.snapshot || len(f.inputs) != 0 {
				t.Fatal("observation must refresh screen without sending input")
			}
		})
	}
}

func TestRuntimeAdapterStoppedPanesNeverSnapshotOrBecomeReady(t *testing.T) {
	for _, state := range []runtime.PaneStatus{runtime.PaneStatusStarting, runtime.PaneStatusExited, runtime.PaneStatusClosed, runtime.PaneStatusLost, runtime.PaneStatusError} {
		t.Run(string(state), func(t *testing.T) {
			f := newAdapterFixture()
			f.pane.Status = state
			observation, err := (RuntimeAdapter{Agents: f, Runtime: f}).Observe(context.Background(), "agent")
			if err != nil || observation.Running || observation.Ready || observation.OwnershipToken != "owner-token" {
				t.Fatalf("inactive observation = %+v, %v", observation, err)
			}
			if !reflect.DeepEqual(f.steps, []string{"explain", "inspect"}) {
				t.Fatalf("inactive pane I/O = %v", f.steps)
			}
		})
	}
}

func TestRuntimeAdapterUnsupportedAgentReturnsActionableError(t *testing.T) {
	for _, kind := range []codingagent.Kind{codingagent.KindGemini, codingagent.KindCursor, codingagent.KindHermes, "unknown"} {
		t.Run(string(kind), func(t *testing.T) {
			f := newAdapterFixture()
			f.before.Kind = kind
			observation, err := (RuntimeAdapter{Agents: f, Runtime: f}).Observe(context.Background(), "agent")
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "Codex") || !strings.Contains(err.Error(), "Claude") || observation.Ready {
				t.Fatalf("unsupported kind result = %+v, %v", observation, err)
			}
			if !reflect.DeepEqual(f.steps, []string{"explain"}) {
				t.Fatalf("unsupported agent reached runtime: %v", f.steps)
			}
		})
	}
}

func TestRuntimeAdapterRejectsChangedIdentityAfterSnapshot(t *testing.T) {
	for _, target := range []string{"agent", "pane"} {
		t.Run(target, func(t *testing.T) {
			f := newAdapterFixture()
			if target == "agent" {
				f.after.AgentID = "replacement-agent"
			} else {
				f.after.PaneID = "replacement-pane"
			}
			observation, err := (RuntimeAdapter{Agents: f, Runtime: f}).Observe(context.Background(), "agent")
			if !errors.Is(err, ErrConflict) || observation.Ready {
				t.Fatalf("identity change result = %+v, %v", observation, err)
			}
		})
	}
}

func TestRuntimeAdapterPropagatesObservationErrors(t *testing.T) {
	sentinel := errors.New("runtime unavailable")
	for _, tc := range []struct {
		name  string
		setup func(*adapterFixture)
		steps []string
	}{
		{"initial explain", func(f *adapterFixture) { f.explainErr = sentinel }, []string{"explain"}},
		{"inspect", func(f *adapterFixture) { f.inspectErr = sentinel }, []string{"explain", "inspect"}},
		{"snapshot", func(f *adapterFixture) { f.snapshotErr = sentinel }, []string{"explain", "inspect", "snapshot"}},
		{"fresh explain", func(f *adapterFixture) { f.afterErr = sentinel }, []string{"explain", "inspect", "snapshot", "explain"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdapterFixture()
			tc.setup(f)
			observation, err := (RuntimeAdapter{Agents: f, Runtime: f}).Observe(context.Background(), "agent")
			if !errors.Is(err, sentinel) || observation.Ready || !reflect.DeepEqual(f.steps, tc.steps) {
				t.Fatalf("error result = %+v, %v, steps = %v", observation, err, f.steps)
			}
		})
	}
}

func TestRuntimeAdapterSendPreservesOwnershipAndAppendsOneNewline(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"후속 지시", "후속 지시\n"},
		{"후속 지시\n", "후속 지시\n"},
		{"후속 지시\n\n", "후속 지시\n"},
		{"first\n\n  second  \n\n", "first\n\n  second  \n"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			f := newAdapterFixture()
			adapter := RuntimeAdapter{Agents: f, Runtime: f}
			err := adapter.Send(context.Background(), Observation{AgentID: "agent", PaneID: "anchored-pane", OwnershipToken: "anchored-token"}, tc.input)
			if err != nil || len(f.inputs) != 1 {
				t.Fatalf("send = %v, inputs = %v", err, f.inputs)
			}
			req := f.inputs[0]
			if req.PaneID != "anchored-pane" || req.OwnershipToken != "anchored-token" || req.Text != tc.want || strings.HasSuffix(req.Text, "\n\n") {
				t.Fatalf("unexpected runtime input: %+v", req)
			}
			if !reflect.DeepEqual(f.steps, []string{"send"}) {
				t.Fatalf("send changed target through extra runtime calls: %v", f.steps)
			}
		})
	}
	f := newAdapterFixture()
	f.sendErr = errors.New("partial write")
	err := (RuntimeAdapter{Agents: f, Runtime: f}).Send(context.Background(), Observation{PaneID: "pane", OwnershipToken: "token"}, "instruction")
	if !errors.Is(err, f.sendErr) || len(f.inputs) != 1 {
		t.Fatal("send error was lost or automatically retried")
	}
}
