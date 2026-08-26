// Package codingagent defines the supported coding-agent command profiles.
package codingagent

import (
	"fmt"
	"strings"
)

type Kind string

const (
	KindCodex  Kind = "codex"
	KindClaude Kind = "claude"
	KindGemini Kind = "gemini"
	KindCursor Kind = "cursor"
	KindHermes Kind = "hermes"
)

type Profile struct {
	Kind        Kind
	DisplayName string
	Executable  string
	BypassArgs  []string
	Manifest    string
	TracksState bool
}

var profiles = map[Kind]Profile{
	KindCodex: {
		Kind:        KindCodex,
		DisplayName: "Codex",
		Executable:  "codex",
		BypassArgs:  []string{"--dangerously-bypass-approvals-and-sandbox"},
		Manifest:    "codex.yaml",
		TracksState: true,
	},
	KindClaude: {
		Kind:        KindClaude,
		DisplayName: "Claude",
		Executable:  "claude",
		BypassArgs:  []string{"--dangerously-skip-permissions"},
		Manifest:    "claude.yaml",
		TracksState: true,
	},
	KindGemini: {
		Kind:        KindGemini,
		DisplayName: "Gemini",
		Executable:  "agy",
		BypassArgs:  []string{"--dangerously-skip-permissions"},
		Manifest:    "gemini.yaml",
		TracksState: true,
	},
	KindCursor: {
		Kind:        KindCursor,
		DisplayName: "Cursor",
		Executable:  "agent",
		BypassArgs:  []string{"--yolo", "--trust"},
		Manifest:    "cursor.yaml",
		TracksState: true,
	},
	KindHermes: {
		Kind:        KindHermes,
		DisplayName: "Hermes",
		Executable:  "hermes",
		TracksState: false,
	},
}

func ParseKind(value string) (Kind, error) {
	kind := Kind(value)
	if _, ok := profiles[kind]; !ok {
		return "", fmt.Errorf("unsupported coding agent kind %q", value)
	}
	return kind, nil
}

func LookupProfile(kind Kind) (Profile, bool) {
	profile, ok := profiles[kind]
	if !ok {
		return Profile{}, false
	}
	profile.BypassArgs = append([]string(nil), profile.BypassArgs...)
	return profile, true
}

func (p Profile) BuildCommand(bypass bool, extra []string) []string {
	args := make([]string, 0, 1+len(p.BypassArgs)+len(extra))
	args = append(args, p.Executable)
	if bypass {
		args = append(args, p.BypassArgs...)
	}
	return append(args, extra...)
}

func (p Profile) BuildManagedCommand(access AccessMode, prompt string, extra []string) ([]string, error) {
	access, err := ParseAccessMode(string(access))
	if err != nil {
		return nil, err
	}
	switch access {
	case AccessFull:
		if prompt != "" {
			return nil, fmt.Errorf("%w: full access does not accept a typed prompt", ErrInvalidAccessMode)
		}
		return p.BuildCommand(true, extra), nil
	case AccessReadOnly:
		if p.Kind != KindCodex || len(extra) != 0 || strings.HasPrefix(prompt, "-") {
			return nil, fmt.Errorf("%w: read-only access is supported only by Codex", ErrInvalidAccessMode)
		}
		args := []string{p.Executable, "--sandbox", "read-only", "--ask-for-approval", "never"}
		if prompt != "" {
			args = append(args, prompt)
		}
		return args, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidAccessMode, access)
	}
}
