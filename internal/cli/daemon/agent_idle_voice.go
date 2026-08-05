package daemoncli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/voice"
)

type agentIdleVoiceStore interface {
	Get(codingagent.ID) (codingagent.Record, error)
}

type agentIdleVoiceQueue interface {
	Enqueue(voice.Notification) (voice.EnqueueStatus, error)
}

func runAgentIdleVoiceLoop(
	ctx context.Context,
	events <-chan eventbus.Event,
	store agentIdleVoiceStore,
	queue agentIdleVoiceQueue,
	log io.Writer,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			handleAgentIdleVoiceEvent(event, store, queue, log)
		}
	}
}

func handleAgentIdleVoiceEvent(
	event eventbus.Event,
	store agentIdleVoiceStore,
	queue agentIdleVoiceQueue,
	log io.Writer,
) {
	if event.Type != eventbus.TypeAgentStateChanged ||
		event.AgentID == "" ||
		event.PreviousState == string(codingagent.StateIdle) ||
		event.AgentState != string(codingagent.StateIdle) {
		return
	}

	record, err := store.Get(codingagent.ID(event.AgentID))
	if errors.Is(err, codingagent.ErrNotFound) {
		return
	}
	if err != nil {
		logAgentIdleVoiceError(log, "agent idle voice lookup %s failed: %v\n", event.AgentID, err)
		return
	}
	if !record.NotifyOnIdle {
		return
	}

	displayName := strings.TrimSpace(string(record.Kind))
	if profile, ok := codingagent.LookupProfile(record.Kind); ok {
		displayName = profile.DisplayName
	}
	if displayName == "" {
		displayName = "Agent"
	}

	_, err = queue.Enqueue(voice.Notification{
		RequestID: fmt.Sprintf("agent-idle:%s:%d", record.ID, record.StateChangedAt.UnixNano()),
		Message:   fmt.Sprintf("%s %s 작업이 완료되었습니다", displayName, record.ID),
	})
	if err != nil {
		logAgentIdleVoiceError(log, "agent idle voice enqueue %s failed: %v\n", event.AgentID, err)
	}
}

func logAgentIdleVoiceError(log io.Writer, format string, args ...any) {
	if log == nil {
		return
	}
	fmt.Fprintf(log, format, args...)
}
