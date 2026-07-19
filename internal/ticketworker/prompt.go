package ticketworker

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

const completionMarkerPrefix = "ZELLIJ_AGENT_TICKET_DONE"

func CompletionMarker(ticketID int64) (string, error) {
	if ticketID <= 0 {
		return "", fmt.Errorf("ticket ID must be positive")
	}
	return completionMarkerPrefix + " " + strconv.FormatInt(ticketID, 10), nil
}

func RenderTicketPrompt(ticket Ticket) (string, string, error) {
	marker, err := CompletionMarker(ticket.ID)
	if err != nil {
		return "", "", err
	}
	body := strings.TrimSpace(ticket.Prompt)
	if body == "" {
		return "", "", ErrInvalidPrompt
	}
	if strings.Contains(body, completionMarkerPrefix) {
		return "", "", ErrInvalidPrompt
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
