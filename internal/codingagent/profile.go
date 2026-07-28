// Package codingagent defines the supported coding-agent command profiles.
package codingagent

import "fmt"

type Kind string

const (
	KindCodex  Kind = "codex"
	KindClaude Kind = "claude"
	KindGemini Kind = "gemini"
	KindCursor Kind = "cursor"
)

type Profile struct {
	Kind        Kind
	DisplayName string
	Executable  string
	BypassArgs  []string
	Manifest    string
}

var profiles = map[Kind]Profile{
	KindCodex: {
		Kind:        KindCodex,
		DisplayName: "Codex",
		Executable:  "codex",
		BypassArgs:  []string{"--dangerously-bypass-approvals-and-sandbox"},
		Manifest:    "codex.yaml",
	},
	KindClaude: {
		Kind:        KindClaude,
		DisplayName: "Claude",
		Executable:  "claude",
		BypassArgs:  []string{"--dangerously-skip-permissions"},
		Manifest:    "claude.yaml",
	},
	KindGemini: {
		Kind:        KindGemini,
		DisplayName: "Gemini",
		Executable:  "agy",
		BypassArgs:  []string{"--dangerously-skip-permissions"},
		Manifest:    "gemini.yaml",
	},
	KindCursor: {
		Kind:        KindCursor,
		DisplayName: "Cursor",
		Executable:  "agent",
		BypassArgs:  []string{"--yolo", "--trust"},
		Manifest:    "cursor.yaml",
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
