package backgrounddebate

import (
	"strings"
	"testing"
)

func TestStagePromptsRequireConciseOutput(t *testing.T) {
	tests := []struct {
		name, prompt, wantLimit, wantRepeat string
	}{
		{name: "proposer", prompt: proposerPrompt("topic", "prior"), wantLimit: "2,000 characters"},
		{name: "critic", prompt: criticPrompt("topic", "proposal"), wantLimit: "2,000 characters", wantRepeat: "Do not quote the proposal at length."},
		{name: "judge", prompt: judgePrompt("topic", "proposal", "critique"), wantLimit: "3,000 characters", wantRepeat: "Do not restate the proposal or critique."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.prompt, tt.wantLimit) {
				t.Fatalf("prompt = %q, want %q", tt.prompt, tt.wantLimit)
			}
			if tt.wantRepeat != "" && !strings.Contains(tt.prompt, tt.wantRepeat) {
				t.Fatalf("prompt = %q, want %q", tt.prompt, tt.wantRepeat)
			}
		})
	}
}
