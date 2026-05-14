package runtime

import (
	"strings"
	"testing"

	"zellij-with-codeagent/internal/eventbus"
)

func TestParseSubscribePaneUpdateJoinsViewportAndScrollback(t *testing.T) {
	line := `{"name":"pane_update","pane_id":"terminal_5","scrollback":["old"],"viewport":["new","line"]}`
	got, err := ParseSubscribeNDJSONLine(line)
	if err != nil {
		t.Fatalf("ParseSubscribeNDJSONLine() error = %v", err)
	}
	if got.Kind != ParsedSubscribePaneUpdate {
		t.Fatalf("Kind = %v, want pane_update", got.Kind)
	}
	if got.ZellijPaneID != "terminal_5" {
		t.Fatalf("ZellijPaneID = %q", got.ZellijPaneID)
	}
	want := "old\nnew\nline"
	if got.RenderedText != want {
		t.Fatalf("RenderedText = %q, want %q", got.RenderedText, want)
	}
}

func TestParseSubscribePaneUpdateNumericPaneID(t *testing.T) {
	line := `{"name":"pane_update","pane_id":7,"viewport":["x"]}`
	got, err := ParseSubscribeNDJSONLine(line)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if got.ZellijPaneID != "terminal_7" {
		t.Fatalf("ZellijPaneID = %q", got.ZellijPaneID)
	}
}

func TestParseSubscribePaneClosed(t *testing.T) {
	line := `{"name":"pane_closed","pane_id":"terminal_5"}`
	got, err := ParseSubscribeNDJSONLine(line)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if got.Kind != ParsedSubscribePaneClosed {
		t.Fatalf("Kind = %v", got.Kind)
	}
	if got.ZellijPaneID != "terminal_5" {
		t.Fatalf("ZellijPaneID = %q", got.ZellijPaneID)
	}
}

func TestParseSubscribeBareContentFallback(t *testing.T) {
	line := `{"pane_id":"terminal_2","content":"hello"}`
	got, err := ParseSubscribeNDJSONLine(line)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if got.Kind != ParsedSubscribePaneUpdate {
		t.Fatalf("Kind = %v", got.Kind)
	}
	if got.RenderedText != "hello" {
		t.Fatalf("RenderedText = %q", got.RenderedText)
	}
}

func TestParseSubscribeMalformedJSONReturnsError(t *testing.T) {
	_, err := ParseSubscribeNDJSONLine(`not-json`)
	if err == nil {
		t.Fatal("want error")
	}
}

func TestParseSubscribeEmptyLineIsUnknown(t *testing.T) {
	got, err := ParseSubscribeNDJSONLine("   ")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if got.Kind != ParsedSubscribeUnknown {
		t.Fatalf("Kind = %v", got.Kind)
	}
}

func TestSemanticEventsFromTextDetectsGoTestSignals(t *testing.T) {
	base := eventbus.Event{
		PaneID:       "p1",
		ZellijPaneID: "terminal_1",
	}

	passText := "\t--- PASS: ExampleTest (0.01s)\n"
	if got := SemanticEventsFromText(passText, base); len(got) != 1 || got[0].Type != eventbus.TypeTestPassed {
		t.Fatalf("want test_passed, got %#v", got)
	}

	failText := "\t--- FAIL: ExampleTest (0.01s)\n"
	if got := SemanticEventsFromText(failText, base); len(got) != 1 || got[0].Type != eventbus.TypeTestFailed {
		t.Fatalf("want test_failed, got %#v", got)
	}
}

func TestSemanticEventsFromTextDetectsServerReady(t *testing.T) {
	base := eventbus.Event{PaneID: "p1"}
	txt := "Application listening on :8080"
	got := SemanticEventsFromText(txt, base)
	var sawReady bool
	for _, e := range got {
		if e.Type == eventbus.TypeServerReady {
			sawReady = true
		}
	}
	if !sawReady {
		t.Fatalf("want server_ready in %#v", got)
	}
}

func TestTrimSnippet(t *testing.T) {
	s := strings.Repeat("a", 600)
	if len(trimSnippet(s, 512)) <= 512 {
		t.Fatal("expected truncation marker")
	}
}
