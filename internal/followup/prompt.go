package followup

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/codingagent"
)

type promptRune struct {
	r     rune
	muted bool
}

type promptLine []promptRune

// A visible idle marker is also present while a person edits a draft. Only an
// empty current composer, or a known visibly styled placeholder, permits input.
// Unknown layouts, suggestions and wrapped placeholders deliberately defer it.
func promptIsEmpty(kind codingagent.Kind, screen string) bool {
	if kind != codingagent.KindCodex && kind != codingagent.KindClaude || len(screen) > 2<<20 {
		return false
	}
	lines := styledPromptLines(screen)
	current := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if _, ok := promptBody(kind, lines[i]); ok {
			current = i
			break
		}
	}
	if current < 0 {
		return false
	}
	body, _ := promptBody(kind, lines[current])
	if !emptyOrPlaceholder(kind, body) {
		return false
	}
	if kind == codingagent.KindClaude {
		return emptyClaudeComposer(lines, current)
	}
	// A marker nested in a multiline draft must not hide the earlier draft text.
	for i := current - 1; i >= 0; i-- {
		plain := promptText(trimPromptLine(lines[i]))
		if plain != "" && strings.ContainsRune("•◦■✗✓─", []rune(plain)[0]) {
			break
		}
		if _, ok := promptBody(kind, lines[i]); ok {
			return false
		}
	}
	for _, line := range lines[current+1:] {
		if len(trimPromptLine(line)) != 0 && !knownPromptFooter(kind, line) {
			return false
		}
	}
	return true
}

func emptyClaudeComposer(lines []promptLine, current int) bool {
	upper, lower := -1, -1
	for i := current - 1; i >= 0; i-- {
		if promptHorizontalRule(lines[i]) {
			upper = i
			break
		}
	}
	for i := current + 1; i < len(lines); i++ {
		if promptHorizontalRule(lines[i]) {
			lower = i
			break
		}
	}
	if upper >= 0 && lower >= 0 {
		// Check the entire input box: the cursor's first line can be empty while
		// later wrapped or explicitly inserted lines still contain a draft.
		for i := upper + 1; i < lower; i++ {
			if i != current && len(stripPromptFrame(lines[i])) != 0 {
				return false
			}
		}
		for _, line := range lines[lower+1:] {
			if len(trimPromptLine(line)) != 0 && !knownPromptFooter(codingagent.KindClaude, line) {
				return false
			}
		}
		return true
	}
	// Legacy unboxed Claude is accepted only with a bare marker, followed by
	// empty rows or positively identified status text. Partial boxes are unknown.
	if strings.Contains(promptText(lines[current]), "│") || upper >= 0 || lower >= 0 {
		return false
	}
	for i := current - 1; i >= 0; i-- {
		plain := promptText(trimPromptLine(lines[i]))
		if plain != "" && strings.ContainsRune("•◦■✗✓─", []rune(plain)[0]) {
			break
		}
		if _, ok := promptBody(codingagent.KindClaude, lines[i]); ok {
			return false
		}
	}
	for _, line := range lines[current+1:] {
		if len(trimPromptLine(line)) != 0 && !knownPromptFooter(codingagent.KindClaude, line) {
			return false
		}
	}
	return true
}

func promptBody(kind codingagent.Kind, line promptLine) (promptLine, bool) {
	line = trimPromptLine(line)
	if kind == codingagent.KindClaude {
		line = stripPromptFrame(line)
	}
	if len(line) == 0 {
		return nil, false
	}
	marker := line[0].r
	if kind == codingagent.KindCodex && marker != '›' && marker != '»' || kind == codingagent.KindClaude && marker != '❯' {
		return nil, false
	}
	if len(line) > 1 && !unicode.IsSpace(line[1].r) {
		return nil, false
	}
	return trimPromptLine(line[1:]), true
}

func emptyOrPlaceholder(kind codingagent.Kind, body promptLine) bool {
	if len(body) == 0 {
		return true
	}
	if !allPromptTextMuted(body) {
		return false
	}
	text := promptText(body)
	if kind == codingagent.KindClaude {
		return text == "Ask Claude"
	}
	switch text {
	case "Use /skills to list available skills", "Find and fix a bug in @filename", "Explain this codebase":
		return true
	default:
		return false
	}
}

var (
	promptShortcutFooter = regexp.MustCompile(`^\? for shortcuts(?:\s+(?:[·•]\s+)?(?:100|[0-9]{1,2})% context left)?$`)
	promptContextFooter  = regexp.MustCompile(`^(?:100|[0-9]{1,2})% context left$`)
	promptModelFooter    = regexp.MustCompile(`^(?:gpt-[A-Za-z0-9.-]+|codex-[A-Za-z0-9.-]+|o[134](?:-[A-Za-z0-9.-]+)?)(?:[ ·]+(?:low|medium|high|xhigh|max|ultra))?\s+[·•]\s+(?:~|/)[^\r\n]+$`)
	promptPathFooter     = regexp.MustCompile(`^(?:~/|/)[^[:space:]]+$`)
	claudeModeFooter     = regexp.MustCompile(`^(?:[⏵▶]+\s+)?(?:bypass permissions on|accept edits on|plan mode on)(?: \(shift\+tab to cycle\))?$`)
)

func knownPromptFooter(kind codingagent.Kind, line promptLine) bool {
	line = trimPromptLine(line)
	// Without styling, a continuation line containing even a familiar footer
	// phrase is indistinguishable from a user's draft and must remain untouched.
	if !allPromptTextMuted(line) {
		return false
	}
	text := promptText(line)
	if promptShortcutFooter.MatchString(text) {
		return true
	}
	if kind == codingagent.KindClaude {
		return claudeModeFooter.MatchString(text)
	}
	return promptContextFooter.MatchString(text) || promptModelFooter.MatchString(text) || promptPathFooter.MatchString(text)
}

func promptHorizontalRule(line promptLine) bool {
	horizontal := 0
	for _, ch := range trimPromptLine(line) {
		switch ch.r {
		case '─', '━':
			horizontal++
		case '╭', '╮', '╰', '╯', '┌', '┐', '└', '┘':
		default:
			return false
		}
	}
	return horizontal >= 3
}

func stripPromptFrame(line promptLine) promptLine {
	line = trimPromptLine(line)
	if len(line) > 0 && line[0].r == '│' {
		line = line[1:]
	}
	if len(line) > 0 && line[len(line)-1].r == '│' {
		line = line[:len(line)-1]
	}
	return trimPromptLine(line)
}

func trimPromptLine(line promptLine) promptLine {
	for len(line) > 0 && unicode.IsSpace(line[0].r) {
		line = line[1:]
	}
	for len(line) > 0 && unicode.IsSpace(line[len(line)-1].r) {
		line = line[:len(line)-1]
	}
	return line
}

func promptText(line promptLine) string {
	var text strings.Builder
	for _, ch := range line {
		text.WriteRune(ch.r)
	}
	return text.String()
}

func allPromptTextMuted(line promptLine) bool {
	for _, ch := range line {
		if !unicode.IsSpace(ch.r) && !ch.muted {
			return false
		}
	}
	return len(line) > 0
}

type promptStyle struct{ dim, italic, gray, inverse bool }

func styledPromptLines(screen string) []promptLine {
	lines := []promptLine{nil}
	style := promptStyle{}
	var state byte
	for len(screen) > 0 {
		seq, _, n, next := ansi.DecodeSequence(screen, state, nil)
		if n == 0 {
			return nil
		}
		screen, state = screen[n:], next
		if strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") {
			style.applySGR(seq[2 : len(seq)-1])
			continue
		}
		for _, r := range ansi.Strip(seq) {
			switch r {
			case '\n':
				lines = append(lines, nil)
			case '\r':
			default:
				lines[len(lines)-1] = append(lines[len(lines)-1], promptRune{r: r, muted: !style.inverse && (style.dim || style.italic || style.gray)})
			}
		}
	}
	return lines
}

func (s *promptStyle) applySGR(parameters string) {
	parts := strings.Split(strings.ReplaceAll(parameters, ":", ";"), ";")
	for i := 0; i < len(parts); i++ {
		code, err := strconv.Atoi(parts[i])
		if parts[i] == "" {
			code, err = 0, nil
		}
		if err != nil {
			*s = promptStyle{}
			return
		}
		switch code {
		case 0:
			*s = promptStyle{}
		case 2:
			s.dim = true
		case 3:
			s.italic = true
		case 7:
			s.inverse = true
		case 22:
			s.dim = false
		case 23:
			s.italic = false
		case 27:
			s.inverse = false
		case 30, 31, 32, 33, 34, 35, 36, 37, 39, 91, 92, 93, 94, 95, 96, 97:
			s.gray = false
		case 90:
			s.gray = true
		case 38, 48, 58:
			// Consume background/underline colors too: their numeric arguments
			// must never accidentally enable dim or italic as standalone codes.
			foreground := code == 38
			if foreground {
				s.gray = false
			}
			if i+2 >= len(parts) {
				return
			}
			mode := parts[i+1]
			if mode == "5" {
				color, _ := strconv.Atoi(parts[i+2])
				if foreground {
					s.gray = color == 8 || color >= 240 && color <= 250
				}
				i += 2
			} else if mode == "2" {
				start := i + 2
				if parts[start] == "" { // Optional colon-form color space.
					start++
				}
				if start+2 >= len(parts) {
					return
				}
				r, _ := strconv.Atoi(parts[start])
				g, _ := strconv.Atoi(parts[start+1])
				b, _ := strconv.Atoi(parts[start+2])
				if foreground {
					s.gray = r == g && g == b && r >= 80 && r <= 190
				}
				i = start + 2
			} else {
				*s = promptStyle{}
				return
			}
		}
	}
}
