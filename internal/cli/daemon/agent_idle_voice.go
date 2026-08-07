package daemoncli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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
	idleTransition := event.PreviousState != string(codingagent.StateIdle) &&
		event.AgentState == string(codingagent.StateIdle)
	initialDetection := event.PreviousState == string(codingagent.StateUnknown)
	if event.Type != eventbus.TypeAgentStateChanged || event.AgentID == "" || initialDetection || !idleTransition {
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

	displayName := ""
	if cwd := strings.TrimSpace(record.CWD); cwd != "" {
		displayName = filepath.Base(filepath.Clean(cwd))
	}
	if displayName == "" {
		displayName = "Agent"
	}

	_, err = queue.Enqueue(voice.Notification{
		RequestID: fmt.Sprintf("agent-idle:%s:%d", record.ID, record.StateChangedAt.UnixNano()),
		Message:   fmt.Sprintf("%s 작업이 완료되었습니다", displayName),
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
