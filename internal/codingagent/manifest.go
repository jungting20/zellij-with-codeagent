package codingagent

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const manifestVersion = 1

type Manifest struct {
	Version                int
	Agent                  Kind
	PreserveStateOnNoMatch bool
	Rules                  []Rule
}

type Rule struct {
	ID              string
	Priority        int
	State           State
	Region          Region
	Matcher         Matcher
	VisibleIdle     bool
	VisibleWorking  bool
	VisibleBlocker  bool
	SkipStateUpdate bool
	Order           int
}

type manifestYAML struct {
	Version                int        `yaml:"version"`
	Agent                  string     `yaml:"agent"`
	PreserveStateOnNoMatch bool       `yaml:"preserve_state_on_no_match"`
	Rules                  []ruleYAML `yaml:"rules"`
}

type ruleYAML struct {
	ID              string      `yaml:"id"`
	Priority        int         `yaml:"priority"`
	State           string      `yaml:"state"`
	Region          regionYAML  `yaml:"region"`
	Match           matcherYAML `yaml:"match"`
	VisibleIdle     bool        `yaml:"visible_idle"`
	VisibleWorking  bool        `yaml:"visible_working"`
	VisibleBlocker  bool        `yaml:"visible_blocker"`
	SkipStateUpdate bool        `yaml:"skip_state_update"`
}

type regionYAML struct {
	Type  string `yaml:"type"`
	Lines int    `yaml:"lines"`
}

type matcherYAML struct {
	Contains  []string      `yaml:"contains"`
	Regex     []string      `yaml:"regex"`
	LineRegex []string      `yaml:"line_regex"`
	All       []matcherYAML `yaml:"all"`
	Any       []matcherYAML `yaml:"any"`
	Not       []matcherYAML `yaml:"not"`
}

func LoadManifest(source []byte) (Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	var raw manifestYAML
	if err := decoder.Decode(&raw); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest YAML: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("decode manifest YAML: multiple documents are not supported")
		}
		return Manifest{}, fmt.Errorf("decode manifest YAML: %w", err)
	}
	if raw.Version != manifestVersion {
		return Manifest{}, fmt.Errorf("unsupported manifest version %d", raw.Version)
	}
	agent, err := ParseKind(strings.TrimSpace(raw.Agent))
	if err != nil {
		return Manifest{}, fmt.Errorf("invalid manifest agent: %w", err)
	}
	if len(raw.Rules) == 0 {
		return Manifest{}, fmt.Errorf("manifest rules must not be empty")
	}

	manifest := Manifest{
		Version:                raw.Version,
		Agent:                  agent,
		PreserveStateOnNoMatch: raw.PreserveStateOnNoMatch,
		Rules:                  make([]Rule, 0, len(raw.Rules)),
	}
	seen := make(map[string]struct{}, len(raw.Rules))
	for order, rawRule := range raw.Rules {
		rule, err := convertRule(rawRule, order)
		if err != nil {
			return Manifest{}, fmt.Errorf("rule %d: %w", order, err)
		}
		if _, ok := seen[rule.ID]; ok {
			return Manifest{}, fmt.Errorf("duplicate rule id %q", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		manifest.Rules = append(manifest.Rules, rule)
	}
	return manifest, nil
}

func convertRule(raw ruleYAML, order int) (Rule, error) {
	id := strings.TrimSpace(raw.ID)
	if id == "" {
		return Rule{}, fmt.Errorf("rule id must not be empty")
	}
	state := State(strings.TrimSpace(raw.State))
	if state == "" && !raw.SkipStateUpdate {
		return Rule{}, fmt.Errorf("state must not be empty")
	}
	if state != "" && !validState(state) {
		return Rule{}, fmt.Errorf("unknown state %q", state)
	}
	region, err := convertRegion(raw.Region)
	if err != nil {
		return Rule{}, err
	}
	matcher, err := compileMatcher(raw.Match)
	if err != nil {
		return Rule{}, fmt.Errorf("matcher: %w", err)
	}
	return Rule{
		ID:              id,
		Priority:        raw.Priority,
		State:           state,
		Region:          region,
		Matcher:         matcher,
		VisibleIdle:     raw.VisibleIdle,
		VisibleWorking:  raw.VisibleWorking,
		VisibleBlocker:  raw.VisibleBlocker,
		SkipStateUpdate: raw.SkipStateUpdate,
		Order:           order,
	}, nil
}
