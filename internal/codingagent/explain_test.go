package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/runtime"
)

func TestExplainRetainsWorkingRevisionWhenStateEventsAreDropped(t *testing.T) {
	f := newMonitorFixture(t)
	bus := eventbus.NewWithBuffer(1)
	t.Cleanup(bus.Close)
	f.monitor.opts.EventBus = bus
	events, unsubscribe := bus.Subscribe(context.Background())
	t.Cleanup(unsubscribe)
	bus.Publish(eventbus.Event{Reason: "fill subscriber buffer"})

	initial, err := f.monitor.Explain(f.record.ID)
	if err != nil || initial.ObservationEpoch == 0 || initial.WorkingRevision != 0 || !initial.LastWorkingAt.IsZero() {
		t.Fatalf("initial observation = %+v, %v", initial, err)
	}
	f.becomeWorking(t)
	firstWorkingAt := f.scheduler.Now()
	f.scheduler.Advance(time.Second)
	// A reason/rule change within Working is not another accepted instruction.
	f.monitor.mu.Lock()
	f.monitor.updateStateLocked(f.monitor.monitoring[f.record.ID], StateUpdate{
		State: StateWorking, Reason: "different working evidence", MatchedRule: "other-working-rule",
	})
	f.monitor.mu.Unlock()
	f.monitor.PaneOutput(f.pane, "READY")
	got, err := f.monitor.Explain(f.record.ID)
	if err != nil || got.State != StateIdle || got.WorkingRevision != 1 ||
		got.ObservationEpoch != initial.ObservationEpoch || !got.LastWorkingAt.Equal(firstWorkingAt) {
		t.Fatalf("completed observation = %+v, %v", got, err)
	}
	if event := <-events; event.Reason != "fill subscriber buffer" {
		t.Fatalf("expected buffered sentinel, got %+v", event)
	}
	select {
	case event := <-events:
		t.Fatalf("expected dropped state events, got %+v", event)
	default:
	}

	f.monitor.PaneOutput(f.pane, "WORK again")
	got, err = f.monitor.Explain(f.record.ID)
	if err != nil || got.WorkingRevision != 2 || !got.LastWorkingAt.Equal(f.scheduler.Now()) {
		t.Fatalf("second working observation = %+v, %v", got, err)
	}
}

func TestExplainWorkingRevisionResetsWhenObservationEpochChanges(t *testing.T) {
	f := newMonitorFixture(t)
	f.becomeWorking(t)
	before, err := f.monitor.Explain(f.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldPane := f.pane
	f.pane.Generation++
	f.monitor.PaneOpened(f.pane)
	f.monitor.PaneOutput(oldPane, "WORK old generation")
	rebound, err := f.monitor.Explain(f.record.ID)
	if err != nil || rebound.ObservationEpoch <= before.ObservationEpoch || rebound.WorkingRevision != 0 ||
		!rebound.LastWorkingAt.IsZero() || rebound.ObservationAvailable {
		t.Fatalf("rebound observation = %+v, %v", rebound, err)
	}
	f.monitor.PaneOutput(f.pane, "READY")
	f.scheduler.Advance(startupGrace)
	f.monitor.PaneOutput(f.pane, "WORK new generation")
	working, err := f.monitor.Explain(f.record.ID)
	if err != nil || working.WorkingRevision != 1 || working.ObservationEpoch != rebound.ObservationEpoch {
		t.Fatalf("new generation working = %+v, %v", working, err)
	}
	f.monitor.PaneError(f.pane, errors.New("observation interrupted"))
	failed, err := f.monitor.Explain(f.record.ID)
	if err != nil || failed.ObservationEpoch <= rebound.ObservationEpoch || failed.WorkingRevision != 0 ||
		!failed.LastWorkingAt.IsZero() || failed.ObservationAvailable {
		t.Fatalf("invalidated observation = %+v, %v", failed, err)
	}
	if err := f.monitor.Start(f.state(t)); err != nil {
		t.Fatal(err)
	}
	restarted, err := f.monitor.Explain(f.record.ID)
	if err != nil || restarted.ObservationEpoch <= failed.ObservationEpoch || restarted.WorkingRevision != 0 {
		t.Fatalf("restarted observation = %+v, %v", restarted, err)
	}
}

func TestExplainReportsPendingStateWithoutChangingMonitor(t *testing.T) {
	f := newMonitorFixture(t)
	service := &Service{monitor: f.monitor}
	initial, err := service.ExplainAgent(context.Background(), f.record.ID)
	if err != nil || initial.ObservationAvailable || initial.Detection != nil || !initial.StartupGrace {
		t.Fatalf("initial explanation = %+v, %v", initial, err)
	}
	f.becomeWorking(t)
	f.monitor.PaneOutput(f.pane, "unrecognized final answer")
	before := f.state(t)
	for i := 0; i < 3; i++ {
		got, err := service.ExplainAgent(context.Background(), f.record.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != StateWorking || got.Detection == nil || got.Detection.Detection.State != StateIdle ||
			!got.Detection.Detection.Fallback || !got.PendingIdle || got.IdleConfirmations != 0 ||
			!got.ObservedAt.Equal(f.scheduler.Now()) {
			t.Fatalf("pending explanation = %+v", got)
		}
	}
	if after := f.state(t); after != before {
		t.Fatalf("Explain changed state: before %+v; after %+v", before, after)
	}
	f.scheduler.Advance(idleConfirmationCount * idleConfirmationDelay)
	got, err := service.ExplainAgent(context.Background(), f.record.ID)
	if err != nil || got.State != StateIdle || got.PendingIdle {
		t.Fatalf("confirmed explanation = %+v, %v", got, err)
	}
	if _, err := service.ExplainAgent(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing error = %v", err)
	}
	if _, err := service.ExplainAgent(context.Background(), " "); !errors.Is(err, ErrAgentIDRequired) {
		t.Fatalf("empty error = %v", err)
	}
}

func TestExplainMatchesDetectorAndBoundsUTF8Evidence(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	if len(loadErrors) != 0 {
		t.Fatal(loadErrors)
	}
	input := DetectionInput{Screen: strings.Repeat("결과", 2000), OSCTitle: "⠸ Working"}
	got, err := detector.Explain(KindCodex, input)
	if err != nil {
		t.Fatal(err)
	}
	want, err := detector.Detect(KindCodex, input)
	if err != nil || got.Detection != want {
		t.Fatalf("explain detection = %+v, direct = %+v, %v", got.Detection, want, err)
	}
	var winning, truncated bool
	for _, rule := range got.Rules {
		if !utf8.ValidString(rule.Evidence) || utf8.RuneCountInString(rule.Evidence) > 1200 {
			t.Fatalf("unbounded or broken UTF-8 evidence for %s", rule.ID)
		}
		if rule.ID == want.RuleID {
			winning = rule.Matched && rule.Evidence == input.OSCTitle
		}
		truncated = truncated || rule.Truncated
	}
	if !winning || !truncated {
		t.Fatalf("missing winning or truncated evidence: %+v", got)
	}
}

func TestMonitorPaneTitleEvidenceAndInvalidation(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	if len(loadErrors) != 0 {
		t.Fatal(loadErrors)
	}
	f := newMonitorFixtureWithDetector(t, detector)
	f.monitor.PaneOutput(f.pane, "unknown screen layout")
	f.monitor.PaneMetadata(f.pane, runtime.PaneMetadata{Title: "⠸ Active task", TitleAvailable: true})
	f.scheduler.Advance(startupGrace)
	if got := f.state(t); got.State != StateWorking || got.MatchedRule != "osc_title_working" {
		t.Fatalf("title did not reach real detector: %+v", got)
	}
	explained, err := f.monitor.Explain(f.record.ID)
	if err != nil || !explained.TitleAvailable || !explained.TitleUsedForDetection ||
		explained.TitleSource != "zellij_pane_title" || explained.ProgressAvailable {
		t.Fatalf("title diagnostics = %+v, %v", explained, err)
	}

	// A custom/default name cannot declare visible Idle and bypass confirmation.
	f.monitor.PaneMetadata(f.pane, runtime.PaneMetadata{Title: "My project", TitleAvailable: true})
	explained, err = f.monitor.Explain(f.record.ID)
	if err != nil || explained.State != StateWorking || !explained.PendingIdle || explained.TitleUsedForDetection {
		t.Fatalf("ordinary title bypassed confirmation: %+v, %v", explained, err)
	}
	f.scheduler.Advance(idleConfirmationCount * idleConfirmationDelay)
	if got := f.state(t); got.State != StateIdle {
		t.Fatalf("stale working persisted: %+v", got)
	}

	f.monitor.PaneMetadata(f.pane, runtime.PaneMetadata{Title: "Action Required", TitleAvailable: true})
	if got := f.state(t); got.State != StateBlocked {
		t.Fatalf("blocked title ignored: %+v", got)
	}
	f.monitor.PaneMetadata(f.pane, runtime.PaneMetadata{})
	explained, err = f.monitor.Explain(f.record.ID)
	if err != nil || explained.TitleAvailable || explained.Title != "" || !explained.TitleObservedAt.IsZero() ||
		explained.State != StateIdle {
		t.Fatalf("unavailable title retained evidence: %+v, %v", explained, err)
	}

	oldPane := f.pane
	f.pane.Generation++
	f.monitor.PaneOpened(f.pane)
	f.monitor.PaneMetadata(oldPane, runtime.PaneMetadata{Title: "⠸ Old work", TitleAvailable: true})
	explained, err = f.monitor.Explain(f.record.ID)
	if err != nil || explained.TitleAvailable || explained.ObservationAvailable || !explained.ObservedAt.IsZero() {
		t.Fatalf("old generation repopulated observation: %+v, %v", explained, err)
	}
	f.monitor.PaneMetadata(f.pane, runtime.PaneMetadata{Title: "⠸ New work", TitleAvailable: true})
	f.monitor.PaneError(f.pane, errors.New("subscription failed"))
	f.scheduler.Advance(time.Second)
	explained, err = f.monitor.Explain(f.record.ID)
	if err != nil || explained.TitleAvailable || explained.ObservationAvailable || explained.State != StateUnknown {
		t.Fatalf("error retained stale observation: %+v, %v", explained, err)
	}
}
