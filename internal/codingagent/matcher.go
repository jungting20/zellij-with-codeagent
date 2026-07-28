package codingagent

import (
	"fmt"
	"regexp"
	"strings"
)

type matcherOperator uint8

const (
	matcherContains matcherOperator = iota + 1
	matcherRegex
	matcherLineRegex
	matcherAll
	matcherAny
	matcherNot
)

type Matcher struct {
	operator matcherOperator
	values   []string
	regexps  []*regexp.Regexp
	children []Matcher
}

func compileMatcher(raw matcherYAML) (Matcher, error) {
	type candidate struct {
		operator matcherOperator
		present  bool
	}
	candidates := []candidate{
		{matcherContains, raw.Contains != nil},
		{matcherRegex, raw.Regex != nil},
		{matcherLineRegex, raw.LineRegex != nil},
		{matcherAll, raw.All != nil},
		{matcherAny, raw.Any != nil},
		{matcherNot, raw.Not != nil},
	}
	var selected matcherOperator
	count := 0
	for _, item := range candidates {
		if item.present {
			selected = item.operator
			count++
		}
	}
	if count == 0 {
		return Matcher{}, fmt.Errorf("matcher must define an operator")
	}
	if count != 1 {
		return Matcher{}, fmt.Errorf("matcher must define exactly one operator")
	}

	matcher := Matcher{operator: selected}
	switch selected {
	case matcherContains:
		if err := requireMatcherValues("contains", raw.Contains); err != nil {
			return Matcher{}, err
		}
		matcher.values = make([]string, len(raw.Contains))
		for i, value := range raw.Contains {
			matcher.values[i] = strings.ToLower(value)
		}
	case matcherRegex:
		compiled, err := compileRegexps("regex", raw.Regex)
		if err != nil {
			return Matcher{}, err
		}
		matcher.regexps = compiled
	case matcherLineRegex:
		compiled, err := compileRegexps("line_regex", raw.LineRegex)
		if err != nil {
			return Matcher{}, err
		}
		matcher.regexps = compiled
	case matcherAll:
		children, err := compileChildren("all", raw.All)
		if err != nil {
			return Matcher{}, err
		}
		matcher.children = children
	case matcherAny:
		children, err := compileChildren("any", raw.Any)
		if err != nil {
			return Matcher{}, err
		}
		matcher.children = children
	case matcherNot:
		children, err := compileChildren("not", raw.Not)
		if err != nil {
			return Matcher{}, err
		}
		matcher.children = children
	}
	return matcher, nil
}

func requireMatcherValues(name string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s matcher must not be empty", name)
	}
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("%s matcher values must not be empty", name)
		}
	}
	return nil
}

func compileRegexps(name string, expressions []string) ([]*regexp.Regexp, error) {
	if err := requireMatcherValues(name, expressions); err != nil {
		return nil, err
	}
	compiled := make([]*regexp.Regexp, 0, len(expressions))
	for _, expression := range expressions {
		re, err := regexp.Compile(expression)
		if err != nil {
			return nil, fmt.Errorf("invalid regexp %q: %w", expression, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

func compileChildren(name string, raw []matcherYAML) ([]Matcher, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s matcher must not be empty", name)
	}
	children := make([]Matcher, 0, len(raw))
	for index, child := range raw {
		compiled, err := compileMatcher(child)
		if err != nil {
			return nil, fmt.Errorf("%s matcher child %d: %w", name, index, err)
		}
		children = append(children, compiled)
	}
	return children, nil
}

func (m Matcher) matches(region string) bool {
	switch m.operator {
	case matcherContains:
		lower := strings.ToLower(region)
		for _, value := range m.values {
			if !strings.Contains(lower, value) {
				return false
			}
		}
		return true
	case matcherRegex:
		for _, re := range m.regexps {
			if !re.MatchString(region) {
				return false
			}
		}
		return true
	case matcherLineRegex:
		lines := strings.Split(region, "\n")
		for _, re := range m.regexps {
			matched := false
			for _, line := range lines {
				if re.MatchString(line) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	case matcherAll:
		for _, child := range m.children {
			if !child.matches(region) {
				return false
			}
		}
		return true
	case matcherAny:
		for _, child := range m.children {
			if child.matches(region) {
				return true
			}
		}
		return false
	case matcherNot:
		for _, child := range m.children {
			if child.matches(region) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
