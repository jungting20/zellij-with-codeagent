package followup

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"zellij-with-codeagent/internal/persistence"
)

func openFollowupWriter(t *testing.T, path string) *persistence.Writer {
	t.Helper()
	w, err := persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func closeFollowupWriter(t *testing.T, w *persistence.Writer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRestartRestoresQueuedEditsCancellationPauseAndRequestIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	w := openFollowupWriter(t, path)
	h := newFollowupHarness(t, SQLiteRepository{Writer: w})
	q := h.update(Update{Action: "add", RequestID: "first-request", Text: "draft"})
	firstID := q.Items[0].ID
	q = h.add("cancel this")
	h.update(Update{Action: "edit", ItemID: firstID, Text: "edited"})
	h.update(Update{Action: "cancel", ItemID: q.Items[1].ID})
	h.update(Update{Action: "pause"})
	closeFollowupWriter(t, w)

	w = openFollowupWriter(t, path)
	defer closeFollowupWriter(t, w)
	restored := newFollowupHarness(t, SQLiteRepository{Writer: w})
	q = restored.queue()
	if q.AgentID != "agent" || q.PaneID != "pane" || q.OwnershipToken != "owner" || q.Phase != Ready || !q.Paused || len(q.Items) != 2 {
		t.Fatalf("restored queue=%+v", q)
	}
	if first := q.Items[0]; first.ID != firstID || first.RequestID != "first-request" || first.State != Queued || first.Text != "edited" || first.CreatedAt.IsZero() {
		t.Fatalf("restored first item=%+v", first)
	}
	if q.Items[1].State != Canceled || q.PendingCount() != 1 {
		t.Fatalf("cancellation did not survive restart: %+v", q.Items)
	}
	restored.step()
	if len(restored.sentTexts()) != 0 {
		t.Fatal("restart lost manual pause")
	}
	q = restored.update(Update{Action: "add", RequestID: "first-request", Text: "edited"})
	if len(q.Items) != 2 {
		t.Fatal("restart lost request deduplication")
	}
	restored.update(Update{Action: "resume"})
	restored.step()
	if sent := restored.sentTexts(); len(sent) != 1 || sent[0] != "edited" {
		t.Fatalf("restored pending instructions dispatched=%v", sent)
	}
}

func TestSQLiteRestartNeverReplaysInFlightInstructions(t *testing.T) {
	for _, phase := range []string{Sending, AwaitingWork, Working} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			w := openFollowupWriter(t, path)
			repo := SQLiteRepository{Writer: w}
			h := newFollowupHarness(t, repo)
			h.add("in-flight")
			h.add("remaining")
			h.step()
			if phase == Working {
				h.observe(func(o *Observation, now time.Time) {
					o.WorkingRevision++
					o.LastWorkingAt, o.ObservedAt = now, now
					o.Ready, o.State = false, "working"
				})
				h.step()
			} else if phase == Sending {
				// Represent a process dying after the durable claim but before the
				// delivery-result write. The runtime input outcome is unknown.
				claim := h.queue()
				claim.Phase, claim.Items[0].State = Sending, Sending
				claim.Items[0].SentAt = time.Time{}
				if err := repo.Save(context.Background(), claim); err != nil {
					t.Fatal(err)
				}
			}
			before, err := repo.Load()
			if err != nil || len(before) != 1 || before[0].Phase != phase {
				t.Fatalf("restart fixture phase=%s, queues=%+v, error=%v", phase, before, err)
			}
			closeFollowupWriter(t, w)

			w = openFollowupWriter(t, path)
			repo = SQLiteRepository{Writer: w}
			restored := newFollowupHarness(t, repo)
			q := restored.queue()
			if q.Phase != NeedsAttention || !q.Paused || q.ActiveItemID != q.Items[0].ID || q.Items[0].State != NeedsAttention || q.Items[1].State != Queued {
				t.Fatalf("in-flight %s restored=%+v", phase, q)
			}
			for i := 0; i < 3; i++ {
				restored.step()
			}
			if len(restored.sentTexts()) != 0 {
				t.Fatalf("restart replayed in-flight %s instruction", phase)
			}
			closeFollowupWriter(t, w)

			// The recovery decision itself must be durable across another restart.
			w = openFollowupWriter(t, path)
			defer closeFollowupWriter(t, w)
			persisted, err := (SQLiteRepository{Writer: w}).Load()
			if err != nil || len(persisted) != 1 || persisted[0].Phase != NeedsAttention || persisted[0].Items[0].Error == "" {
				t.Fatalf("recovery decision not committed: queues=%+v, error=%v", persisted, err)
			}
		})
	}
}
