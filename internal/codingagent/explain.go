package codingagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"zellij-with-codeagent/internal/runtime"
)

// AgentExplanation describes the running monitor, including a pending transition.
// Observation timestamps, revisions, title metadata and evidence are transient:
// recovery obtains new runtime observations instead of persisting detection input.
// Revisions are comparable only within the same daemon lifetime and observation epoch.
type AgentExplanation struct {
	AgentID               ID                    `json:"agent_id"`
	Kind                  Kind                  `json:"kind"`
	PaneID                runtime.PaneID        `json:"pane_id"`
	State                 State                 `json:"state"`
	StateReason           string                `json:"state_reason"`
	MatchedRule           string                `json:"matched_rule,omitempty"`
	StateChangedAt        time.Time             `json:"state_changed_at"`
	ObservedAt            time.Time             `json:"observed_at"`
	ObservationEpoch      uint64                `json:"observation_epoch"`
	WorkingRevision       uint64                `json:"working_revision"`
	LastWorkingAt         time.Time             `json:"last_working_at"`
	ObservationAvailable  bool                  `json:"observation_available"`
	StartupGrace          bool                  `json:"startup_grace"`
	PendingIdle           bool                  `json:"pending_idle"`
	IdleConfirmations     int                   `json:"idle_confirmations"`
	TitleAvailable        bool                  `json:"title_available"`
	Title                 string                `json:"title,omitempty"`
	TitleSource           string                `json:"title_source,omitempty"`
	TitleUsedForDetection bool                  `json:"title_used_for_detection"`
	TitleObservedAt       time.Time             `json:"title_observed_at"`
	ProgressAvailable     bool                  `json:"progress_available"`
	Detection             *DetectionExplanation `json:"detection,omitempty"`
}

type DetectionExplanation struct {
	Detection Detection        `json:"detection"`
	Rules     []RuleEvaluation `json:"rules"`
}

type RuleEvaluation struct {
	ID        string `json:"id"`
	Priority  int    `json:"priority"`
	Region    Region `json:"region"`
	Matched   bool   `json:"matched"`
	Evidence  string `json:"evidence"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Explain uses the same detector as monitoring. Rule evidence is bounded while
// matching always uses the full region, including evidence outside the excerpt.
func (d *Detector) Explain(kind Kind, input DetectionInput) (DetectionExplanation, error) {
	detection, err := d.Detect(kind, input)
	if err != nil {
		return DetectionExplanation{}, err
	}
	explanation := DetectionExplanation{Detection: detection, Rules: make([]RuleEvaluation, 0, len(d.rules[kind]))}
	for _, rule := range d.rules[kind] {
		region := selectRegion(rule.Region, input)
		evidence, truncated := detectionEvidence(region)
		explanation.Rules = append(explanation.Rules, RuleEvaluation{
			ID: rule.ID, Priority: rule.Priority, Region: rule.Region,
			Matched: rule.Matcher.matches(region), Evidence: evidence, Truncated: truncated,
		})
	}
	return explanation, nil
}

func detectionEvidence(region string) (string, bool) {
	const maxEvidenceRunes = 1200
	runes := []rune(region)
	if len(runes) <= maxEvidenceRunes {
		return region, false
	}
	return string(runes[len(runes)-maxEvidenceRunes:]), true
}

func (m *Monitor) Explain(id ID) (AgentExplanation, error) {
	if m == nil || m.opts.Store == nil {
		return AgentExplanation{}, ErrAgentMonitorRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.opts.Store.Get(id)
	if err != nil {
		return AgentExplanation{}, err
	}
	explanation := AgentExplanation{
		AgentID: record.ID, Kind: record.Kind, PaneID: record.PaneID,
		State: record.State, StateReason: record.StateReason,
		MatchedRule: record.MatchedRule, StateChangedAt: record.StateChangedAt,
	}
	entry := m.monitoring[id]
	if !sameMonitoredRecord(entry, record) {
		return explanation, nil
	}
	explanation.ObservedAt = entry.observedAt
	explanation.ObservationEpoch = entry.token
	explanation.WorkingRevision = entry.workingRevision
	explanation.LastWorkingAt = entry.lastWorkingAt
	explanation.ObservationAvailable = entry.hasInput
	explanation.StartupGrace = entry.graceTimer != nil
	explanation.PendingIdle = entry.idleTimer != nil || entry.idleDeadline != nil
	explanation.IdleConfirmations = entry.idleConfirmations
	explanation.TitleAvailable = entry.titleAvailable
	explanation.Title = entry.paneTitle
	explanation.TitleUsedForDetection = entry.latestInput.OSCTitle != ""
	explanation.TitleObservedAt = entry.titleObservedAt
	if entry.titleAvailable {
		explanation.TitleSource = "zellij_pane_title"
	}
	// Zellij subscriptions do not expose raw OSC progress; do not manufacture it
	// from pane output or claim that an unconnected detector field is supported.
	explanation.ProgressAvailable = false
	if !entry.hasInput {
		return explanation, nil
	}
	detection, err := m.opts.Detector.Explain(record.Kind, entry.latestInput)
	if err != nil {
		return AgentExplanation{}, err
	}
	explanation.Detection = &detection
	return explanation, nil
}

func (s *Service) ExplainAgent(ctx context.Context, id ID) (AgentExplanation, error) {
	if err := ctx.Err(); err != nil {
		return AgentExplanation{}, err
	}
	id = ID(strings.TrimSpace(string(id)))
	if id == "" {
		return AgentExplanation{}, ErrAgentIDRequired
	}
	explainer, ok := s.monitor.(interface {
		Explain(ID) (AgentExplanation, error)
	})
	if !ok {
		return AgentExplanation{}, fmt.Errorf("%w: live diagnostics unavailable", ErrAgentMonitorRequired)
	}
	return explainer.Explain(id)
}
