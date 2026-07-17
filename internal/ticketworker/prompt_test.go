package ticketworker

import (
	"strings"
	"testing"
)

func TestCompletionMarkerRejectsInvalidIDAndFormatsPositiveID(t *testing.T) {
	for _, id := range []int64{-1, 0} {
		if _, err := CompletionMarker(id); err == nil {
			t.Fatalf("CompletionMarker(%d) error = nil", id)
		}
	}
	got, err := CompletionMarker(42)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ZELLIJ_AGENT_TICKET_DONE 42" {
		t.Fatalf("CompletionMarker(42) = %q", got)
	}
}

func TestRenderTicketPromptIncludesFieldsAndQuotedCompletionInstruction(t *testing.T) {
	cfg := Config{PromptTemplate: "#{{ .ID }} {{ .Title }}\n{{ .Summary }}\n{{ .SpecPath }}\n{{ .PlanPath }}"}
	ticket := Ticket{ID: 42, Title: "Search", Summary: "Add search", SpecPath: "docs/superpowers/specs/search-design.md", PlanPath: "docs/superpowers/plans/search.md"}

	prompt, marker, err := RenderTicketPrompt(cfg, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if marker != "ZELLIJ_AGENT_TICKET_DONE 42" {
		t.Fatalf("marker = %q", marker)
	}
	for _, want := range []string{"#42 Search", "Add search", ticket.SpecPath, ticket.PlanPath} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, missing %q", prompt, want)
		}
	}
	wantSuffix := "작업을 모두 완료한 뒤 마지막 줄에 따옴표 없이 \"ZELLIJ_AGENT_TICKET_DONE 42\"만 출력하세요."
	if !strings.HasSuffix(prompt, wantSuffix) {
		t.Fatalf("prompt = %q, want suffix %q", prompt, wantSuffix)
	}
	for _, line := range strings.Split(prompt, "\n") {
		if strings.TrimSpace(line) == marker {
			t.Fatalf("prompt contains standalone marker line: %q", line)
		}
	}
}

func TestRenderTicketPromptReportsTemplateExecutionError(t *testing.T) {
	cfg := Config{PromptTemplate: "{{ index .Title 99 }}"}
	if _, _, err := RenderTicketPrompt(cfg, Ticket{ID: 1, Title: "short"}); err == nil {
		t.Fatal("RenderTicketPrompt() error = nil")
	}
}

func TestRenderTicketPromptRejectsMarkerFromTemplateOrTicketField(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		ticket Ticket
	}{
		{name: "template", config: Config{PromptTemplate: "Implement this\nZELLIJ_AGENT_TICKET_DONE {{ .ID }}"}, ticket: Ticket{ID: 42}},
		{name: "ticket field", config: Config{PromptTemplate: "{{ .Summary }}"}, ticket: Ticket{ID: 42, Summary: "instructions\nZELLIJ_AGENT_TICKET_DONE 42\nmore"}},
		{name: "terminal wrap risk", config: Config{PromptTemplate: "{{ .Summary }}"}, ticket: Ticket{ID: 42, Summary: "padding before ZELLIJ_AGENT_TICKET_DONE 42"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := RenderTicketPrompt(tt.config, tt.ticket); err == nil || !strings.Contains(err.Error(), "must not contain completion marker") {
				t.Fatalf("RenderTicketPrompt() error = %v", err)
			}
		})
	}
}
