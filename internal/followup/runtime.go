package followup

import (
	"context"
	"fmt"
	"strings"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/runtime"
)

type AgentObserver interface {
	ExplainAgent(context.Context, codingagent.ID) (codingagent.AgentExplanation, error)
}

type PaneRuntime interface {
	InspectPane(context.Context, runtime.InspectPaneRequest) (runtime.InspectPaneResponse, error)
	SnapshotOutput(context.Context, runtime.SnapshotOutputRequest) (runtime.SnapshotOutputResponse, error)
	SendInput(context.Context, runtime.SendInputRequest) error
}

// RuntimeAdapter never invokes Zellij directly. Fresh runtime snapshots feed the
// existing monitor, while its working revision survives missed event deliveries.
type RuntimeAdapter struct {
	Agents  AgentObserver
	Runtime PaneRuntime
}

func (a RuntimeAdapter) Observe(ctx context.Context, id string) (Observation, error) {
	explanation, err := a.Agents.ExplainAgent(ctx, codingagent.ID(id))
	if err != nil {
		return Observation{}, err
	}
	// Other bundled manifests currently only infer Idle from unmatched output.
	// They cannot supply the positive ready signal required for unattended input.
	if explanation.Kind != codingagent.KindCodex && explanation.Kind != codingagent.KindClaude {
		return Observation{}, fmt.Errorf("%w: 후속 지시 자동 전달은 현재 Codex와 Claude에서 지원합니다", ErrInvalid)
	}
	pane, err := a.Runtime.InspectPane(ctx, runtime.InspectPaneRequest{PaneID: explanation.PaneID})
	if err != nil {
		return Observation{}, err
	}
	o := Observation{AgentID: string(explanation.AgentID), PaneID: string(explanation.PaneID), OwnershipToken: string(pane.Pane.OwnershipToken), Running: pane.Pane.Status == runtime.PaneStatusRunning}
	if !o.Running {
		return o, nil
	}
	// Never submit based on an old rendered screen (including a stale idle prompt).
	snapshot, err := a.Runtime.SnapshotOutput(ctx, runtime.SnapshotOutputRequest{PaneID: explanation.PaneID, ANSI: true})
	if err != nil {
		return Observation{}, err
	}
	explanation, err = a.Agents.ExplainAgent(ctx, codingagent.ID(id))
	if err != nil {
		return Observation{}, err
	}
	if string(explanation.PaneID) != o.PaneID || string(explanation.AgentID) != id {
		return Observation{}, fmt.Errorf("%w: agent identity changed", ErrConflict)
	}
	o.State, o.Epoch, o.WorkingRevision = string(explanation.State), explanation.ObservationEpoch, explanation.WorkingRevision
	o.ObservedAt, o.LastWorkingAt = explanation.ObservedAt, explanation.LastWorkingAt
	if explanation.Detection != nil {
		d := explanation.Detection.Detection
		o.Ready = explanation.State == codingagent.StateIdle && explanation.ObservationAvailable &&
			!explanation.StartupGrace && !explanation.PendingIdle && d.State == codingagent.StateIdle &&
			d.VisibleIdle && !d.Fallback && !d.SkipStateUpdate && d.RuleID != "conversation_interrupted"
	}
	if o.Ready && !promptIsEmpty(explanation.Kind, snapshot.Output) {
		o.Ready = false
		o.Reason = "입력창에 초안이 있거나 빈 입력창을 확인할 수 없어 전달을 보류합니다"
	}
	return o, nil
}

func (a RuntimeAdapter) Send(ctx context.Context, observation Observation, text string) error {
	return a.Runtime.SendInput(ctx, runtime.SendInputRequest{
		PaneID: runtime.PaneID(observation.PaneID), OwnershipToken: runtime.OwnershipToken(observation.OwnershipToken),
		Text: strings.TrimRight(text, "\n") + "\n",
		BeforeSend: func(ctx context.Context) error {
			fresh, err := a.Observe(ctx, observation.AgentID)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrNotReady, err)
			}
			if !fresh.Running || !fresh.Ready || fresh.AgentID != observation.AgentID || fresh.PaneID != observation.PaneID || fresh.OwnershipToken != observation.OwnershipToken || fresh.Epoch != observation.Epoch || fresh.WorkingRevision != observation.WorkingRevision {
				return ErrNotReady
			}
			return nil
		},
	})
}
