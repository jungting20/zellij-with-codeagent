package codingagent

import (
	"fmt"
	"strings"
)

type RegionType string

const (
	RegionWholeRecent             RegionType = "whole_recent"
	RegionBottomNonEmptyLines     RegionType = "bottom_non_empty_lines"
	RegionAfterLastPromptMarker   RegionType = "after_last_prompt_marker"
	RegionPromptBoxBody           RegionType = "prompt_box_body"
	RegionAfterLastHorizontalRule RegionType = "after_last_horizontal_rule"
	RegionOSCTitle                RegionType = "osc_title"
	RegionOSCProgress             RegionType = "osc_progress"
)

type Region struct {
	Type  RegionType
	Lines int
}

func convertRegion(raw regionYAML) (Region, error) {
	region := Region{Type: RegionType(strings.TrimSpace(raw.Type)), Lines: raw.Lines}
	switch region.Type {
	case RegionWholeRecent, RegionAfterLastPromptMarker, RegionPromptBoxBody,
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
		if index := strings.LastIndex(input.Screen, "›"); index >= 0 {
			return input.Screen[index+len("›"):]
		}
		return input.Screen
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
