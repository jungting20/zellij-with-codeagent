package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"zellij-with-codeagent/internal/transport"
)

var ErrInvalidExecutionPlanEnvelope = errors.New("planner: invalid execution plan envelope")

type ValidatedExecutionPlan struct {
	Envelope transport.RequestEnvelope
	Payload  transport.ExecutionPlanPayload
}

func ParseExecutionPlanEnvelope(data []byte) (ValidatedExecutionPlan, error) {
	plan, err := DecodeExecutionPlanEnvelope(data)
	if err != nil {
		return ValidatedExecutionPlan{}, err
	}
	if err := ValidateExecutionPlan(plan); err != nil {
		return ValidatedExecutionPlan{}, err
	}
	return plan, nil
}

func DecodeExecutionPlanEnvelope(data []byte) (ValidatedExecutionPlan, error) {
	var envelope transport.RequestEnvelope
	if err := decodeStrict(data, &envelope); err != nil {
		return ValidatedExecutionPlan{}, fmt.Errorf("%w: %v", ErrInvalidExecutionPlanEnvelope, err)
	}
	if envelope.Type == "" {
		return ValidatedExecutionPlan{}, fmt.Errorf("%w: type is required", ErrInvalidExecutionPlanEnvelope)
	}
	if envelope.Type != transport.RequestTypeExecutionPlan {
		return ValidatedExecutionPlan{}, fmt.Errorf("%w: unsupported request type %q", ErrInvalidExecutionPlanEnvelope, envelope.Type)
	}
	if envelope.RequestID == "" {
		return ValidatedExecutionPlan{}, fmt.Errorf("%w: request_id is required", ErrInvalidExecutionPlanEnvelope)
	}
	if len(envelope.Payload) == 0 {
		return ValidatedExecutionPlan{}, fmt.Errorf("%w: payload is required", ErrInvalidExecutionPlanEnvelope)
	}

	var payload *transport.ExecutionPlanPayload
	if err := decodeStrict(envelope.Payload, &payload); err != nil {
		return ValidatedExecutionPlan{}, fmt.Errorf("%w: invalid payload: %v", ErrInvalidExecutionPlanEnvelope, err)
	}
	if payload == nil {
		return ValidatedExecutionPlan{}, fmt.Errorf("%w: invalid payload: object is required", ErrInvalidExecutionPlanEnvelope)
	}

	return ValidatedExecutionPlan{Envelope: envelope, Payload: *payload}, nil
}

func ValidateExecutionPlan(plan ValidatedExecutionPlan) error {
	return validateExecutionPlanPayload(plan.Payload)
}

func validateExecutionPlanPayload(payload transport.ExecutionPlanPayload) error {
	if payload.Session == "" {
		return fmt.Errorf("%w: payload.session is required", ErrInvalidExecutionPlanEnvelope)
	}
	if strings.TrimSpace(payload.ZellijSession) == "" {
		return fmt.Errorf("%w: payload.zellij_session is required", ErrInvalidExecutionPlanEnvelope)
	}
	if len(payload.Tabs) == 0 {
		return fmt.Errorf("%w: payload.tabs must contain at least one tab", ErrInvalidExecutionPlanEnvelope)
	}

	seen := make(map[string]struct{})
	for _, tab := range payload.Tabs {
		if len(tab.Panes) == 0 {
			return fmt.Errorf("%w: tab %q must contain at least one pane", ErrInvalidExecutionPlanEnvelope, tab.Name)
		}
		for _, pane := range tab.Panes {
			if pane.ID == "" {
				return fmt.Errorf("%w: pane id is required", ErrInvalidExecutionPlanEnvelope)
			}
			if _, ok := seen[pane.ID]; ok {
				return fmt.Errorf("%w: duplicate pane id %q", ErrInvalidExecutionPlanEnvelope, pane.ID)
			}
			seen[pane.ID] = struct{}{}
		}
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
