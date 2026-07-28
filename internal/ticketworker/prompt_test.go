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
	for _, want := range []string{
		"마지막 두 줄",
		`"ZELLIJ_AGENT_TICKET_SUMMARY"`,
		`"ZELLIJ_AGENT_TICKET_DONE 42"`,
		"실제 출력에는 따옴표를 포함하지 마세요",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want instruction containing %q", prompt, want)
		}
	}
}

func TestRenderTicketPromptViewportEchoCannotParseAsCompletion(t *testing.T) {
	prompt, marker, err := RenderTicketPrompt(Ticket{ID: 42, Prompt: "Implement search."})
	if err != nil {
		t.Fatal(err)
	}
	viewport := "terminal header\n• " + strings.ReplaceAll(prompt, "\n", "\n• ") + "\n›"

	if done, summary := parseCompletionOutput(viewport, marker); done || summary != "" {
		t.Fatalf("parseCompletionOutput(rendered prompt, %q) = (%t, %q), want (false, empty)", marker, done, summary)
	}
}

func TestParseCompletionOutputFindsRealCompletionAfterPromptEcho(t *testing.T) {
	prompt, marker, err := RenderTicketPrompt(Ticket{ID: 42, Prompt: "Implement search."})
	if err != nil {
		t.Fatal(err)
	}
	viewport := "terminal header\n• " + strings.ReplaceAll(prompt, "\n", "\n• ")
	output := viewport + "\nZELLIJ_AGENT_TICKET_SUMMARY 실제 변경\n" + marker

	done, summary := parseCompletionOutput(output, marker)
	if !done || summary != "실제 변경" {
		t.Fatalf("parseCompletionOutput(prompt plus real completion, %q) = (%t, %q), want (true, 실제 변경)", marker, done, summary)
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

func TestRenderTicketPromptAllowsMarkerNamesInStoredInstructions(t *testing.T) {
	ticket := Ticket{ID: 42, Prompt: "ZELLIJ_AGENT_TICKET_DONE은 완료 마커 접두사입니다."}

	if _, _, err := RenderTicketPrompt(ticket); err != nil {
		t.Fatalf("RenderTicketPrompt(%q) error = %v, want nil", ticket.Prompt, err)
	}
}

func TestParseCompletionOutput(t *testing.T) {
	marker := "ZELLIJ_AGENT_TICKET_DONE 42"
	tests := []struct {
		name    string
		output  string
		done    bool
		summary string
	}{
		{
			name:    "selects nearest preceding nonempty summary",
			output:  "ZELLIJ_AGENT_TICKET_SUMMARY 이전 요약\nZELLIJ_AGENT_TICKET_SUMMARY 최종 변경\nZELLIJ_AGENT_TICKET_DONE 42",
			done:    true,
			summary: "최종 변경",
		},
		{
			name:   "supports marker only output",
			output: marker,
			done:   true,
		},
		{
			name:   "treats empty summary as absent",
			output: "ZELLIJ_AGENT_TICKET_SUMMARY   \n" + marker,
			done:   true,
		},
		{
			name:    "skips empty summary when finding nearest nonempty summary",
			output:  "ZELLIJ_AGENT_TICKET_SUMMARY 유효한 요약\nZELLIJ_AGENT_TICKET_SUMMARY   \n" + marker,
			done:    true,
			summary: "유효한 요약",
		},
		{
			name:   "rejects wrong marker",
			output: "ZELLIJ_AGENT_TICKET_SUMMARY 변경\nZELLIJ_AGENT_TICKET_DONE 43",
			done:   false,
		},
		{
			name:    "accepts display bullet prefix",
			output:  "• ZELLIJ_AGENT_TICKET_SUMMARY 변경\n• ZELLIJ_AGENT_TICKET_DONE 42",
			done:    true,
			summary: "변경",
		},
		{
			name:   "does not select summary after marker",
			output: marker + "\nZELLIJ_AGENT_TICKET_SUMMARY 나중 요약",
			done:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			done, summary := parseCompletionOutput(test.output, marker)
			if done != test.done || summary != test.summary {
				t.Fatalf("parseCompletionOutput(%q, %q) = (%t, %q), want (%t, %q)", test.output, marker, done, summary, test.done, test.summary)
			}
		})
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
