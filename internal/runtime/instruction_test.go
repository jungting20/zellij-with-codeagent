package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"zellij-with-codeagent/internal/registry"
)

func TestLastInstructionRecordsOnlySuccessfulSubmittedInput(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	reg := registry.NewWithClock(func() time.Time { return now })
	backend := &fakeBackend{createID: "terminal_1"}
	service := NewService(Options{Registry: reg, Backend: backend})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane", ZellijSession: "test"})
	const text = "  원인을 확인해줘.\n테스트도 실행해줘.  "
	if err := service.SendInput(context.Background(), SendInputRequest{PaneID: "pane", Text: text + "\n"}); err != nil {
		t.Fatal(err)
	}
	originalTime := now
	for _, tc := range []struct {
		text string
		fail bool
	}{
		{"\n", false}, {" \t\n", false}, {"partial", false}, {"failed\n", true},
	} {
		now = now.Add(time.Minute)
		backend.sendErr = nil
		if tc.fail {
			backend.sendErr = errors.New("send failed")
		}
		err := service.SendInput(context.Background(), SendInputRequest{PaneID: "pane", Text: tc.text})
		if (err != nil) != tc.fail {
			t.Fatalf("SendInput(%q) = %v", tc.text, err)
		}
		got, err := service.InspectPane(context.Background(), InspectPaneRequest{PaneID: "pane"})
		if err != nil || got.Pane.LastInstruction != text || !got.Pane.LastInstructionAt.Equal(originalTime) {
			t.Fatalf("last instruction changed: %+v, %v", got.Pane, err)
		}
	}
	backend.sendErr = nil
	if err := service.SendInput(context.Background(), SendInputRequest{PaneID: "pane", Text: "next\n"}); err != nil {
		t.Fatal(err)
	}
	got, _ := reg.GetPane("pane")
	if got.LastInstruction != "next" || !got.LastInstructionAt.Equal(now) {
		t.Fatalf("new instruction not recorded: %+v", got)
	}
}
