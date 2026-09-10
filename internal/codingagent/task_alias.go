package codingagent

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TaskAlias is a preset or custom work label, independent of the agent's live state.
type TaskAlias string

const (
	TaskAliasNone      TaskAlias = ""
	TaskAliasImplement TaskAlias = "implement"
	TaskAliasBugfix    TaskAlias = "bugfix"
	TaskAliasRefactor  TaskAlias = "refactor"
	TaskAliasTest      TaskAlias = "test"
	TaskAliasReview    TaskAlias = "review"
	TaskAliasDocs      TaskAlias = "docs"
	TaskAliasResearch  TaskAlias = "research"
)

var ErrInvalidTaskAlias = errors.New("invalid coding agent task alias")

func TaskAliases() []TaskAlias {
	return []TaskAlias{TaskAliasNone, TaskAliasImplement, TaskAliasBugfix, TaskAliasRefactor, TaskAliasTest, TaskAliasReview, TaskAliasDocs, TaskAliasResearch}
}

func (alias TaskAlias) Valid() bool {
	value := string(alias)
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

func (alias TaskAlias) Label() string {
	switch alias {
	case TaskAliasNone:
		return "미지정"
	case TaskAliasImplement:
		return "구현"
	case TaskAliasBugfix:
		return "버그 수정"
	case TaskAliasRefactor:
		return "리팩터링"
	case TaskAliasTest:
		return "테스트"
	case TaskAliasReview:
		return "리뷰"
	case TaskAliasDocs:
		return "문서"
	case TaskAliasResearch:
		return "조사"
	default:
		return string(alias)
	}
}
