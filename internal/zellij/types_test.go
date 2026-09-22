package zellij

import (
	"encoding/json"
	"testing"
)

func TestPaneTitleAvailabilityDoesNotReusePreviousTitle(t *testing.T) {
	var pane Pane
	for _, tt := range []struct {
		name      string
		payload   string
		title     string
		available bool
	}{
		{"working", `{"id":24,"title":"⠸ Working | project"}`, "⠸ Working | project", true},
		{"missing", `{"id":24}`, "", false},
		{"idle", `{"id":24,"title":"project"}`, "project", true},
		{"null", `{"id":24,"title":null}`, "", false},
		{"empty", `{"id":24,"title":""}`, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tt.payload), &pane); err != nil {
				t.Fatal(err)
			}
			if pane.ID != "terminal_24" || pane.Title != tt.title || pane.TitleAvailable != tt.available {
				t.Fatalf("pane = %#v, want title %q, available %t", pane, tt.title, tt.available)
			}
		})
	}
}
