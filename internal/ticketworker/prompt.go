package ticketworker

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

const completionMarkerPrefix = "ZELLIJ_AGENT_TICKET_DONE"
const completionSummaryPrefix = "ZELLIJ_AGENT_TICKET_SUMMARY"

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
	if done, _ := parseCompletionOutput(body, marker); done {
		return "", "", ErrInvalidPrompt
	}
	instruction := fmt.Sprintf("작업을 모두 완료한 뒤 마지막 두 줄에 아래 내용을 순서대로 출력하세요.\n%s 실제 변경 사항을 한 줄로 간결하게 요약\n%s", completionSummaryPrefix, marker)
	return body + "\n\n" + instruction, marker, nil
}

func containsExactLine(output, marker string) bool {
	done, _ := parseCompletionOutput(output, marker)
	return done
}

func parseCompletionOutput(output, marker string) (done bool, summary string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := normalizeCompletionLine(scanner.Text())
		if line == marker {
			return true, summary
		}
		if strings.HasPrefix(line, completionSummaryPrefix+" ") {
			if value := strings.TrimSpace(strings.TrimPrefix(line, completionSummaryPrefix+" ")); value != "" {
				summary = value
			}
		}
	}
	return false, ""
}

func normalizeCompletionLine(line string) string {
	line = strings.TrimSpace(line)
	return strings.TrimSpace(strings.TrimPrefix(line, "• "))
}
