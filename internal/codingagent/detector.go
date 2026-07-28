package codingagent

import (
	"fmt"
	"sort"
)

type DetectionInput struct {
	Screen      string
	OSCTitle    string
	OSCProgress string
}

type Detection struct {
	State           State
	RuleID          string
	Reason          string
	VisibleIdle     bool
	VisibleWorking  bool
	VisibleBlocker  bool
	SkipStateUpdate bool
	Fallback        bool
}

type Detector struct {
	rules map[Kind][]Rule
}

func NewDetector(manifests map[Kind]Manifest) (*Detector, error) {
	detector := &Detector{rules: make(map[Kind][]Rule, len(manifests))}
	for kind, manifest := range manifests {
		if kind != manifest.Agent {
			return nil, fmt.Errorf("detector map key %q does not match manifest agent %q", kind, manifest.Agent)
		}
		if _, ok := LookupProfile(kind); !ok {
			return nil, fmt.Errorf("unsupported detector kind %q", kind)
		}
		rules := append([]Rule(nil), manifest.Rules...)
		sort.SliceStable(rules, func(i, j int) bool {
			if rules[i].Priority == rules[j].Priority {
				return rules[i].Order < rules[j].Order
			}
			return rules[i].Priority > rules[j].Priority
		})
		detector.rules[kind] = rules
	}
	return detector, nil
}

func (d *Detector) Detect(kind Kind, input DetectionInput) (Detection, error) {
	if d == nil {
		return Detection{}, fmt.Errorf("detector is nil")
	}
	rules, ok := d.rules[kind]
	if !ok {
		return Detection{}, fmt.Errorf("no manifest configured for coding agent kind %q", kind)
	}
	for _, rule := range rules {
		region := selectRegion(rule.Region, input)
		if !rule.Matcher.matches(region) {
			continue
		}
		state := rule.State
		if rule.SkipStateUpdate {
			state = ""
		}
		return Detection{
			State:           state,
			RuleID:          rule.ID,
			Reason:          "matched_manifest_rule",
			VisibleIdle:     rule.VisibleIdle,
			VisibleWorking:  rule.VisibleWorking,
			VisibleBlocker:  rule.VisibleBlocker,
			SkipStateUpdate: rule.SkipStateUpdate,
		}, nil
	}
	return Detection{
		State:    StateIdle,
		Reason:   "default_known_agent_idle_fallback",
		Fallback: true,
	}, nil
}
