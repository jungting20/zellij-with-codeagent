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
			"layout": "triple-horizontal",
			"tabs": [
				{"name": "page-example", "panes": [{"id": "page-editor", "role": "editor"}]}
			]
		}
	}`))
	if err != nil {
		t.Fatalf("ParseExecutionPlanEnvelope() error = %v", err)
	}
	if plan.Envelope.RequestID != "req_page" || plan.Payload.Session != "page-example" {
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
