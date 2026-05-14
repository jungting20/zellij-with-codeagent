package runtime

import (
	"encoding/json"
	"strconv"
	"strings"

	"zellij-with-codeagent/internal/eventbus"
)

// ParsedSubscribeKind classifies a line from zellij subscribe NDJSON output.
type ParsedSubscribeKind int

const (
	ParsedSubscribeUnknown ParsedSubscribeKind = iota
	ParsedSubscribePaneUpdate
	ParsedSubscribePaneClosed
)

// ParsedSubscribeLine is one decoded subscribe frame from Zellij.
type ParsedSubscribeLine struct {
	Kind         ParsedSubscribeKind
	ZellijPaneID string
	RenderedText string
}

type subscribeEnvelope struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Event      string          `json:"event"`
	PaneID     json.RawMessage `json:"pane_id"`
	Viewport   []string        `json:"viewport"`
	Scrollback []string        `json:"scrollback"`
	Lines      []string        `json:"lines"`
	Content    string          `json:"content"`
}

// ParseSubscribeNDJSONLine decodes one NDJSON line from zellij subscribe --format json.
func ParseSubscribeNDJSONLine(line string) (ParsedSubscribeLine, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return ParsedSubscribeLine{}, nil
	}

	var env subscribeEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return ParsedSubscribeLine{}, err
	}

	kindStr := env.Name
	if kindStr == "" {
		kindStr = env.Type
	}
	if kindStr == "" {
		kindStr = env.Event
	}
	kindStr = strings.ToLower(kindStr)

	paneID, err := parseSubscribePaneID(env.PaneID)
	if err != nil && env.PaneID != nil && string(env.PaneID) != "null" && strings.TrimSpace(string(env.PaneID)) != "" {
		return ParsedSubscribeLine{}, err
	}

	switch kindStr {
	case "pane_update", "paneupdate":
		text := joinSubscribeText(env.Viewport, env.Scrollback, env.Lines, env.Content)
		return ParsedSubscribeLine{
			Kind:         ParsedSubscribePaneUpdate,
			ZellijPaneID: paneID,
			RenderedText: text,
		}, nil
	case "pane_closed", "paneclosed":
		return ParsedSubscribeLine{
			Kind:         ParsedSubscribePaneClosed,
			ZellijPaneID: paneID,
		}, nil
	default:
		// Architecture-note examples sometimes used bare content/pane_id without a named event.
		if strings.TrimSpace(env.Content) != "" && paneID != "" {
			return ParsedSubscribeLine{
				Kind:         ParsedSubscribePaneUpdate,
				ZellijPaneID: paneID,
				RenderedText: env.Content,
			}, nil
		}
		return ParsedSubscribeLine{Kind: ParsedSubscribeUnknown}, nil
	}
}

func joinSubscribeText(viewport, scrollback, lines []string, content string) string {
	var b strings.Builder
	first := true
	writeBlock := func(block []string) {
		for _, s := range block {
			s = strings.TrimRight(s, "\r")
			if !first {
				b.WriteByte('\n')
			}
			first = false
			b.WriteString(s)
		}
	}
	writeBlock(scrollback)
	writeBlock(viewport)
	writeBlock(lines)
	if strings.TrimSpace(content) != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(content)
	}
	return b.String()
}

func parseSubscribePaneID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text), nil
	}

	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return "terminal_" + strconv.Itoa(number), nil
	}

	return "", nil
}

// SemanticEventsFromText derives coarse semantic events from rendered viewport text.
// rawEvt carries pane identifiers for emitted events.
func SemanticEventsFromText(text string, rawEvt eventbus.Event) []eventbus.Event {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var out []eventbus.Event
	lower := strings.ToLower(text)

	if strings.Contains(lower, "listening on ") ||
		strings.Contains(lower, "server listening") ||
		strings.Contains(lower, "accepting connections") ||
		strings.Contains(text, ":3000") ||
		strings.Contains(lower, " ready ") ||
		strings.HasSuffix(lower, " ready") {
		e := rawEvt
		e.Type = eventbus.TypeServerReady
		e.Message = trimSnippet(text, 512)
		out = append(out, e)
	}

	if strings.Contains(text, "--- FAIL:") ||
		strings.Contains(text, "\nFAIL\t") ||
		strings.Contains(text, "\nFAIL ") {
		e := rawEvt
		e.Type = eventbus.TypeTestFailed
		e.Message = trimSnippet(text, 512)
		out = append(out, e)
	}

	if strings.Contains(text, "--- PASS:") ||
		strings.Contains(text, "\nPASS\t") ||
		strings.Contains(text, "\nok ") {
		e := rawEvt
		e.Type = eventbus.TypeTestPassed
		e.Message = trimSnippet(text, 512)
		out = append(out, e)
	}

	return out
}

func trimSnippet(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
