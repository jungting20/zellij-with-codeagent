package codingagent

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type RegionType string

const (
	RegionWholeRecent               RegionType = "whole_recent"
	RegionBottomNonEmptyLines       RegionType = "bottom_non_empty_lines"
	RegionAfterLastPromptMarker     RegionType = "after_last_prompt_marker"
	RegionBeforeCurrentPromptMarker RegionType = "before_current_prompt_marker"
	RegionCurrentPromptMarker       RegionType = "current_prompt_marker"
	RegionPromptBoxBody             RegionType = "prompt_box_body"
	RegionAfterLastHorizontalRule   RegionType = "after_last_horizontal_rule"
	RegionOSCTitle                  RegionType = "osc_title"
	RegionOSCProgress               RegionType = "osc_progress"
)

type Region struct {
	Type  RegionType `json:"type"`
	Lines int        `json:"lines,omitempty"`
}

func convertRegion(raw regionYAML) (Region, error) {
	region := Region{Type: RegionType(strings.TrimSpace(raw.Type)), Lines: raw.Lines}
	switch region.Type {
	case RegionWholeRecent, RegionAfterLastPromptMarker, RegionBeforeCurrentPromptMarker, RegionCurrentPromptMarker, RegionPromptBoxBody,
		RegionAfterLastHorizontalRule, RegionOSCTitle, RegionOSCProgress:
		return region, nil
	case RegionBottomNonEmptyLines:
		if region.Lines <= 0 {
			return Region{}, fmt.Errorf("bottom_non_empty_lines lines must be greater than zero")
		}
		return region, nil
	default:
		return Region{}, fmt.Errorf("unknown region %q", region.Type)
	}
}

func selectRegion(region Region, input DetectionInput) string {
	switch region.Type {
	case RegionWholeRecent:
		return input.Screen
	case RegionBottomNonEmptyLines:
		return bottomNonEmptyLines(input.Screen, region.Lines)
	case RegionAfterLastPromptMarker:
		lines := strings.Split(input.Screen, "\n")
		if index := lastPromptLine(lines); index >= 0 {
			return strings.Join(lines[index+1:], "\n")
		}
		return input.Screen
	case RegionBeforeCurrentPromptMarker:
		lines := strings.Split(input.Screen, "\n")
		if index := currentPromptLine(lines); index >= 0 {
			return strings.Join(lines[:index], "\n")
		}
		return input.Screen
	case RegionCurrentPromptMarker:
		lines := strings.Split(input.Screen, "\n")
		if index := currentPromptLine(lines); index >= 0 {
			return lines[index]
		}
		return ""
	case RegionPromptBoxBody:
		return promptBoxBody(input.Screen)
	case RegionAfterLastHorizontalRule:
		return afterLastHorizontalRule(input.Screen)
	case RegionOSCTitle:
		return input.OSCTitle
	case RegionOSCProgress:
		return input.OSCProgress
	default:
		return ""
	}
}

// Prompt boundaries are complete lines, not quoted marker characters in output.
// Codex's prompt animation can replace the space after either marker with braille.
func lastPromptLine(lines []string) int {
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		marker, size := utf8.DecodeRuneInString(line)
		if marker != '›' && marker != '»' {
			continue
		}
		rest := line[size:]
		separator, _ := utf8.DecodeRuneInString(rest)
		if rest == "" || unicode.IsSpace(separator) || (separator >= '\u2800' && separator <= '\u28ff') {
			return index
		}
	}
	return -1
}

func currentPromptLine(lines []string) int {
	index := lastPromptLine(lines)
	if index < 0 {
		return -1
	}
	// A subsequent response/status means this is a submitted, historical prompt.
	for _, line := range lines[index+1:] {
		first, _ := utf8.DecodeRuneInString(strings.TrimSpace(line))
		if strings.ContainsRune("•◦■✗✓─", first) {
			return -1
		}
	}
	return index
}

func bottomNonEmptyLines(screen string, count int) string {
	if count <= 0 {
		return ""
	}
	lines := strings.Split(screen, "\n")
	remaining := count
	start := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		remaining--
		if remaining == 0 {
			start = i
			return strings.Join(lines[start:], "\n")
		}
	}
	return screen
}

func promptBoxBody(screen string) string {
	lines := strings.Split(screen, "\n")
	for upper := len(lines) - 1; upper >= 0; upper-- {
		if !strings.Contains(lines[upper], "╭") || !isHorizontalRule(lines[upper]) {
			continue
		}
		for lower := upper + 1; lower < len(lines); lower++ {
			if isHorizontalRule(lines[lower]) {
				return strings.Join(lines[upper+1:lower], "\n")
			}
		}
		return strings.Join(lines[upper+1:], "\n")
	}
	return screen
}

func afterLastHorizontalRule(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if isHorizontalRule(lines[i]) {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return screen
}

func isHorizontalRule(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	horizontal := 0
	for _, r := range line {
		switch r {
		case '─', '━', '-', '=', '╭', '╮', '╰', '╯', '├', '┤', '┬', '┴', '┼', '┌', '┐', '└', '┘':
			if r == '─' || r == '━' || r == '-' || r == '=' {
				horizontal++
			}
		default:
			return false
		}
	}
	return horizontal >= 3
}
