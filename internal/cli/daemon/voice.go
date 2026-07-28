package daemoncli

import (
	"context"
	"errors"
	"io"

	"zellij-with-codeagent/internal/transport"
	"zellij-with-codeagent/internal/voice"
)

type daemonVoiceService interface {
	Enqueue(voice.Notification) (voice.EnqueueStatus, error)
	Close() error
}

var newDaemonVoiceService = func(stdout io.Writer) daemonVoiceService {
	return voice.NewNativeService(stdout)
}

type voiceQueueAdapter struct {
	service daemonVoiceService
}

func (a voiceQueueAdapter) QueueVoiceNotification(ctx context.Context, req transport.VoiceNotificationRequest) (transport.VoiceNotificationResponse, error) {
	if err := ctx.Err(); err != nil {
		return transport.VoiceNotificationResponse{}, err
	}

	status, err := a.service.Enqueue(voice.Notification{
		RequestID: req.RequestID,
		Prefix:    req.Prefix,
		TicketID:  req.TicketID,
		Summary:   req.Summary,
	})
	if errors.Is(err, voice.ErrQueueFull) {
		return transport.VoiceNotificationResponse{}, transport.ErrVoiceQueueFull
	}
	if err != nil {
		return transport.VoiceNotificationResponse{}, err
	}
	return transport.VoiceNotificationResponse{Status: string(status)}, nil
}
