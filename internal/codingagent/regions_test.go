package codingagent

import "testing"

func TestPromptRegionsRecognizeOnlyCompletePromptLines(t *testing.T) {
	for _, marker := range []string{"›", "»"} {
		for _, separator := range []string{" ", "\t", "⠁", "⢀ "} {
			prompt := "  " + marker + separator + "Ask Codex to do anything"
			screen := "• Working (12s • esc to interrupt)\n\n" + prompt + "\n  ⠠⢀\n  model footer"
			t.Run(marker+separator, func(t *testing.T) {
				input := DetectionInput{Screen: screen}
				for _, test := range []struct {
					region RegionType
					want   string
				}{
					{RegionBeforeCurrentPromptMarker, "• Working (12s • esc to interrupt)\n"},
					{RegionCurrentPromptMarker, prompt},
					{RegionAfterLastPromptMarker, "  ⠠⢀\n  model footer"},
				} {
					if got := selectRegion(Region{Type: test.region}, input); got != test.want {
						t.Fatalf("selectRegion(%s) = %q, want %q", test.region, got, test.want)
					}
				}
			})
		}
	}
}

func TestPromptRegionsDoNotTruncateQuotedMarkersOrSubmittedPrompts(t *testing.T) {
	for _, screen := range []string{
		"The answer quotes › and » characters.\n• Working (12s)",
		"›not a prompt\n• Working (12s)",
		"› previous request\n• Working (12s)",
		"»⠁previous request\n  ◦ Thinking (12s)",
		"› previous request\n■ Conversation interrupted",
		"» previous request\n✓ Finished",
	} {
		t.Run(screen, func(t *testing.T) {
			input := DetectionInput{Screen: screen}
			if got := selectRegion(Region{Type: RegionBeforeCurrentPromptMarker}, input); got != screen {
				t.Fatalf("before current prompt = %q, want entire screen", got)
			}
			if got := selectRegion(Region{Type: RegionCurrentPromptMarker}, input); got != "" {
				t.Fatalf("current prompt = %q, want empty", got)
			}
		})
	}
}

func TestAfterLastPromptMarkerIgnoresQuotedMarkerAndPromptContents(t *testing.T) {
	screen := "› earlier request\nOld response\n»⠁allow command?\nA quotation contains › here\npress enter to confirm or esc to cancel"
	want := "A quotation contains › here\npress enter to confirm or esc to cancel"
	if got := selectRegion(Region{Type: RegionAfterLastPromptMarker}, DetectionInput{Screen: screen}); got != want {
		t.Fatalf("after last prompt = %q, want %q", got, want)
	}
}
