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
	instruction := fmt.Sprintf("작업을 모두 완료한 뒤 최종 출력의 마지막 두 줄을 다음 형식으로 작성하세요. 첫째 줄은 %q 다음에 실제 변경 사항을 한 줄로 간결하게 요약하고, 둘째 줄은 정확히 %q로 작성하세요. 여기의 따옴표는 설명용이며 실제 출력에는 따옴표를 포함하지 마세요.", completionSummaryPrefix, marker)
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
