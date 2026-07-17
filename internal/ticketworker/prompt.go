package ticketworker

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

const completionMarkerPrefix = "ZELLIJ_AGENT_TICKET_DONE"

func CompletionMarker(ticketID int64) (string, error) {
	if ticketID <= 0 {
		return "", fmt.Errorf("ticket ID must be positive")
	}
	return completionMarkerPrefix + " " + strconv.FormatInt(ticketID, 10), nil
}

func RenderTicketPrompt(cfg Config, ticket Ticket) (string, string, error) {
	marker, err := CompletionMarker(ticket.ID)
	if err != nil {
		return "", "", err
	}
	parsed, err := template.New("ticket-prompt").Option("missingkey=error").Parse(cfg.PromptTemplate)
	if err != nil {
		return "", "", fmt.Errorf("parse prompt template: %w", err)
	}
	var rendered bytes.Buffer
	if err := parsed.Execute(&rendered, ticket); err != nil {
		return "", "", fmt.Errorf("render prompt template: %w", err)
	}
	body := strings.TrimRight(rendered.String(), " \t\r\n")
	if body == "" {
		return "", "", fmt.Errorf("rendered prompt must not be empty")
	}
	if containsExactLine(body, marker) {
		return "", "", fmt.Errorf("rendered prompt must not contain completion marker %q as a standalone line", marker)
	}
	instruction := fmt.Sprintf("작업을 모두 완료한 뒤 마지막 줄에 따옴표 없이 %q만 출력하세요.", marker)
	return body + "\n\n" + instruction, marker, nil
}

func containsExactLine(output, marker string) bool {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == marker {
			return true
		}
	}
	return false
}
