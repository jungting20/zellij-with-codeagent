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

func TestRenderTicketPromptAppendsCompletionInstruction(t *testing.T) {
	ticket := Ticket{ID: 42, Prompt: "Implement search.\nPreserve this layout."}

	prompt, marker, err := RenderTicketPrompt(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if marker != "ZELLIJ_AGENT_TICKET_DONE 42" {
		t.Fatalf("marker = %q", marker)
	}
	if !strings.HasPrefix(prompt, ticket.Prompt+"\n\n") {
		t.Fatalf("prompt = %q, want stored prompt prefix", prompt)
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

func TestRenderTicketPromptRejectsInvalidStoredPrompt(t *testing.T) {
	tests := []Ticket{
		{ID: 42, Prompt: ""},
		{ID: 42, Prompt: "   "},
		{ID: 42, Prompt: "instructions\nZELLIJ_AGENT_TICKET_DONE 42\nmore"},
	}
	for _, ticket := range tests {
		if _, _, err := RenderTicketPrompt(ticket); err == nil {
			t.Fatalf("RenderTicketPrompt(%q) error = nil", ticket.Prompt)
		}
	}
}

func TestContainsExactLineAcceptsCodexAnswerBullet(t *testing.T) {
	marker := "ZELLIJ_AGENT_TICKET_DONE 3"
	output := "Hello World 3\n\n• ZELLIJ_AGENT_TICKET_DONE 3\n"

	if !containsExactLine(output, marker) {
		t.Fatalf("containsExactLine(%q, %q) = false, want true", output, marker)
	}
}

func TestContainsExactLineRejectsUnknownMarkerPrefixes(t *testing.T) {
	marker := "ZELLIJ_AGENT_TICKET_DONE 3"
	for _, output := range []string{
		"prefix ZELLIJ_AGENT_TICKET_DONE 3",
		"> ZELLIJ_AGENT_TICKET_DONE 3",
		"• prefix ZELLIJ_AGENT_TICKET_DONE 3",
	} {
		if containsExactLine(output, marker) {
			t.Fatalf("containsExactLine(%q, %q) = true, want false", output, marker)
		}
	}
}
