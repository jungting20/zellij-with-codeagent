package planner

import (
	"errors"
	"testing"
)

func TestParseExecutionPlanEnvelopeAcceptsCanonicalEnvelope(t *testing.T) {
	plan, err := ParseExecutionPlanEnvelope([]byte(`{
		"type": "execution_plan",
		"request_id": "req_page",
		"payload": {
			"session": "page-example",
			"zellij_session": "physical-a",
			"layout": "triple-horizontal",
			"tabs": [
				{"name": "page-example", "panes": [{"id": "page-editor", "role": "editor"}]}
			]
		}
	}`))
	if err != nil {
		t.Fatalf("ParseExecutionPlanEnvelope() error = %v", err)
	}
	if plan.Envelope.RequestID != "req_page" || plan.Payload.Session != "page-example" || plan.Payload.ZellijSession != "physical-a" {
		t.Fatalf("plan = %#v, want request and payload", plan)
	}
}

func TestParseExecutionPlanEnvelopeRejectsLegacyPayloadFields(t *testing.T) {
	_, err := ParseExecutionPlanEnvelope([]byte(`{
		"type": "execution_plan",
		"request_id": "req_page",
		"payload": {
			"url": "http://localhost:8000/example/aa",
			"resolved_source": "/tmp/app/src/pages/example/aa.tsx",
			"session": "page-example",
			"zellij_session": "physical-a",
			"tabs": [
				{"name": "page-example", "panes": [{"id": "page-editor"}]}
			]
		}
	}`))
	if !errors.Is(err, ErrInvalidExecutionPlanEnvelope) {
		t.Fatalf("ParseExecutionPlanEnvelope() error = %v, want ErrInvalidExecutionPlanEnvelope", err)
	}
}

func TestParseExecutionPlanEnvelopeRejectsDuplicatePaneIDs(t *testing.T) {
	_, err := ParseExecutionPlanEnvelope([]byte(`{
		"type": "execution_plan",
		"request_id": "req_page",
		"payload": {
			"session": "page-example",
			"zellij_session": "physical-a",
			"tabs": [
				{"name": "page-example", "panes": [{"id": "page-editor"}, {"id": "page-editor"}]}
			]
		}
	}`))
	if !errors.Is(err, ErrInvalidExecutionPlanEnvelope) {
		t.Fatalf("ParseExecutionPlanEnvelope() error = %v, want ErrInvalidExecutionPlanEnvelope", err)
	}
}

func TestParseExecutionPlanEnvelopeAllowsArbitraryLayoutMetadata(t *testing.T) {
	plan, err := ParseExecutionPlanEnvelope([]byte(`{
		"type": "execution_plan",
		"request_id": "req_page",
		"payload": {
			"session": "page-example",
			"zellij_session": "physical-a",
			"layout": "custom-grid",
			"tabs": [
				{"name": "page-example", "panes": [{"id": "page-editor"}]}
			]
		}
	}`))
	if err != nil {
		t.Fatalf("ParseExecutionPlanEnvelope() error = %v", err)
	}
	if plan.Payload.Layout != "custom-grid" {
		t.Fatalf("payload layout = %q, want custom-grid", plan.Payload.Layout)
	}
}

func TestDecodeExecutionPlanEnvelopeAllowsMissingZellijSession(t *testing.T) {
	plan, err := DecodeExecutionPlanEnvelope([]byte(`{
		"type":"execution_plan",
		"request_id":"req_page",
		"payload":{"session":"page-example","tabs":[{"panes":[{"id":"page-editor"}]}]}
	}`))
	if err != nil {
		t.Fatalf("DecodeExecutionPlanEnvelope() error = %v", err)
	}
	if plan.Payload.Session != "page-example" {
		t.Fatalf("payload session = %q, want page-example", plan.Payload.Session)
	}
}

func TestDecodeExecutionPlanEnvelopeRejectsUnknownPayloadFields(t *testing.T) {
	_, err := DecodeExecutionPlanEnvelope([]byte(`{
		"type":"execution_plan",
		"request_id":"req_page",
		"payload":{"session":"page-example","zellij_session":"physical-a","unknown":true}
	}`))
	if !errors.Is(err, ErrInvalidExecutionPlanEnvelope) {
		t.Fatalf("DecodeExecutionPlanEnvelope() error = %v, want ErrInvalidExecutionPlanEnvelope", err)
	}
}

func TestDecodeExecutionPlanEnvelopeRejectsNullPayload(t *testing.T) {
	_, err := DecodeExecutionPlanEnvelope([]byte(`{
		"type":"execution_plan",
		"request_id":"req_page",
		"payload":null
	}`))
	if !errors.Is(err, ErrInvalidExecutionPlanEnvelope) {
		t.Fatalf("DecodeExecutionPlanEnvelope() error = %v, want ErrInvalidExecutionPlanEnvelope", err)
	}
}

func TestValidateExecutionPlanRejectsMissingZellijSession(t *testing.T) {
	plan, err := DecodeExecutionPlanEnvelope([]byte(`{
		"type":"execution_plan",
		"request_id":"req_page",
		"payload":{"session":"page-example","tabs":[{"panes":[{"id":"page-editor"}]}]}
	}`))
	if err != nil {
		t.Fatalf("DecodeExecutionPlanEnvelope() error = %v", err)
	}
	if err := ValidateExecutionPlan(plan); !errors.Is(err, ErrInvalidExecutionPlanEnvelope) {
		t.Fatalf("ValidateExecutionPlan() error = %v, want ErrInvalidExecutionPlanEnvelope", err)
	}
}

func TestValidateExecutionPlanRejectsWhitespaceZellijSession(t *testing.T) {
	plan, err := DecodeExecutionPlanEnvelope([]byte(`{
		"type":"execution_plan",
		"request_id":"req_page",
		"payload":{"session":"page-example","zellij_session":"   ","tabs":[{"panes":[{"id":"page-editor"}]}]}
	}`))
	if err != nil {
		t.Fatalf("DecodeExecutionPlanEnvelope() error = %v", err)
	}
	if err := ValidateExecutionPlan(plan); !errors.Is(err, ErrInvalidExecutionPlanEnvelope) {
		t.Fatalf("ValidateExecutionPlan() error = %v, want ErrInvalidExecutionPlanEnvelope", err)
	}
}

func TestParseExecutionPlanEnvelopeRejectsMissingZellijSession(t *testing.T) {
	_, err := ParseExecutionPlanEnvelope([]byte(`{
		"type":"execution_plan",
		"request_id":"req_page",
		"payload":{"session":"page-example","tabs":[{"panes":[{"id":"page-editor"}]}]}
	}`))
	if !errors.Is(err, ErrInvalidExecutionPlanEnvelope) {
		t.Fatalf("ParseExecutionPlanEnvelope() error = %v, want ErrInvalidExecutionPlanEnvelope", err)
	}
}
