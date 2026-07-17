package ticketworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 7, 16, 12, 0, 0, 123, time.UTC)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(context.Background(), root, filepath.Join(root, ".local", "tickets.db"), func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, root
}

func writeArtifacts(t *testing.T, root, name string) (string, string) {
	t.Helper()
	spec := filepath.Join(root, "docs", "superpowers", "specs", name+"-design.md")
	plan := filepath.Join(root, "docs", "superpowers", "plans", name+".md")
	for _, path := range []string{spec, plan} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# Approved\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return spec, plan
}

func TestOpenCreatesSchemaAndAddRegistersReadyTicket(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "search")

	got, err := store.Add(context.Background(), CreateInput{
		Title:    "Search story bible",
		Summary:  "Add indexed story-bible search.",
		SpecPath: spec,
		PlanPath: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusReady || got.ID != 1 {
		t.Fatalf("Add() = %#v", got)
	}
	if got.SpecPath != "docs/superpowers/specs/search-design.md" || got.PlanPath != "docs/superpowers/plans/search.md" {
		t.Fatalf("stored paths = %q, %q", got.SpecPath, got.PlanPath)
	}
	if !got.CreatedAt.Equal(fixedNow) || !got.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("timestamps = %s, %s", got.CreatedAt, got.UpdatedAt)
	}
}

func TestOpenInitializesSchemaIdempotently(t *testing.T) {
	store, root := newTestStore(t)
	databasePath := filepath.Join(root, ".local", "tickets.db")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	spec, plan := writeArtifacts(t, root, "idempotent")
	if _, err := second.Add(context.Background(), CreateInput{
		Title:    "Idempotent schema",
		Summary:  "Open an initialized ticket database.",
		SpecPath: spec,
		PlanPath: plan,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsUnsupportedFutureSchemaVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, ".local", "tickets.db")
	store, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow }); err == nil {
		t.Fatal("Open() error = nil, want unsupported future schema rejection")
	} else if got, want := err.Error(), "unsupported ticket schema version 2"; got != want {
		t.Fatalf("Open() error = %q, want %q", got, want)
	}
}

func TestAddAcceptsRepositoryRelativeArtifactPaths(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "relative")
	relativeSpec, err := filepath.Rel(root, spec)
	if err != nil {
		t.Fatal(err)
	}
	relativePlan, err := filepath.Rel(root, plan)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Add(context.Background(), CreateInput{
		Title:    "Relative artifacts",
		Summary:  "Register repository-relative artifact paths.",
		SpecPath: relativeSpec,
		PlanPath: relativePlan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SpecPath != "docs/superpowers/specs/relative-design.md" || got.PlanPath != "docs/superpowers/plans/relative.md" {
		t.Fatalf("stored paths = %q, %q", got.SpecPath, got.PlanPath)
	}
}

func TestAddRejectsDuplicatePlanWithoutInserting(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "search")
	input := CreateInput{Title: "Search", Summary: "Search stories.", SpecPath: spec, PlanPath: plan}
	if _, err := store.Add(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	_, err := store.Add(context.Background(), input)
	if !errors.Is(err, ErrDuplicatePlan) {
		t.Fatalf("Add() error = %v, want ErrDuplicatePlan", err)
	}
}

func TestAddRejectsInvalidArtifacts(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "search")
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "docs", "superpowers", "plans", "outside.md")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Fatal(err)
	}

	cases := []CreateInput{
		{Title: "", Summary: "summary", SpecPath: spec, PlanPath: plan},
		{Title: "title", Summary: "", SpecPath: spec, PlanPath: plan},
		{Title: "title", Summary: "summary", SpecPath: filepath.Join(root, "missing.md"), PlanPath: plan},
		{Title: "title", Summary: "summary", SpecPath: spec, PlanPath: outside},
		{Title: "title", Summary: "summary", SpecPath: spec, PlanPath: symlink},
	}
	for _, input := range cases {
		if _, err := store.Add(context.Background(), input); !errors.Is(err, ErrInvalidArtifact) {
			t.Fatalf("Add(%#v) error = %v, want ErrInvalidArtifact", input, err)
		}
	}
}

func TestGetReturnsTicketAndRejectsUnknownID(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "get")
	created, err := store.Add(context.Background(), CreateInput{
		Title:    "Get ticket",
		Summary:  "Retrieve one ticket.",
		SpecPath: spec,
		PlanPath: plan,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != created {
		t.Fatalf("Get() = %#v, want %#v", got, created)
	}
	if _, err := store.Get(context.Background(), created.ID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestListFiltersStatusesAndOrdersFIFOTiesByID(t *testing.T) {
	store, root := newTestStore(t)
	firstSpec, firstPlan := writeArtifacts(t, root, "first")
	first, err := store.Add(context.Background(), CreateInput{Title: "First", Summary: "First summary", SpecPath: firstSpec, PlanPath: firstPlan})
	if err != nil {
		t.Fatal(err)
	}
	secondSpec, secondPlan := writeArtifacts(t, root, "second")
	second, err := store.Add(context.Background(), CreateInput{Title: "Second", Summary: "Second summary", SpecPath: secondSpec, PlanPath: secondPlan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), second.ID, ActionStart); err != nil {
		t.Fatal(err)
	}

	all, err := store.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != first.ID || all[1].ID != second.ID {
		t.Fatalf("List(nil) = %#v", all)
	}

	ready, err := store.List(context.Background(), statusPtr(StatusReady))
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].ID != first.ID {
		t.Fatalf("List(ready) = %#v", ready)
	}

	inProgress, err := store.List(context.Background(), statusPtr(StatusInProgress))
	if err != nil {
		t.Fatal(err)
	}
	if len(inProgress) != 1 || inProgress[0].ID != second.ID {
		t.Fatalf("List(in_progress) = %#v", inProgress)
	}

	invalid := Status("unknown")
	if _, err := store.List(context.Background(), &invalid); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("List(unknown) error = %v, want ErrInvalidStatus", err)
	}
}

func TestListReturnsAllocatedEmptySlice(t *testing.T) {
	store, _ := newTestStore(t)

	got, err := store.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List(nil) = %#v, want allocated empty slice", got)
	}
}

func TestNextClaimsOldestReady(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := fixedNow
	store, err := Open(context.Background(), root, filepath.Join(root, ".local", "tickets.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spec1, plan1 := writeArtifacts(t, root, "first")
	first, err := store.Add(context.Background(), CreateInput{Title: "First", Summary: "First summary", SpecPath: spec1, PlanPath: plan1})
	if err != nil {
		t.Fatal(err)
	}
	spec2, plan2 := writeArtifacts(t, root, "second")
	if _, err := store.Add(context.Background(), CreateInput{Title: "Second", Summary: "Second summary", SpecPath: spec2, PlanPath: plan2}); err != nil {
		t.Fatal(err)
	}

	now = fixedNow.Add(time.Minute)
	got, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || got.Status != StatusInProgress || got.StartedAt == nil || !got.StartedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("Next() = %#v", got)
	}
	after, err := store.Get(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, got) {
		t.Fatalf("claimed ticket not persisted: returned=%#v stored=%#v", got, after)
	}
	remaining, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if remaining.ID == first.ID || remaining.Status != StatusInProgress {
		t.Fatalf("second Next() = %#v", remaining)
	}
}

func TestNextReturnsEmptyQueueAfterReadyTicketsLeaveQueue(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "first")
	created, err := store.Add(context.Background(), CreateInput{Title: "First", Summary: "First summary", SpecPath: spec, PlanPath: plan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), created.ID, ActionStart); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Next(context.Background()); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("Next() error = %v, want ErrEmptyQueue", err)
	}
}

func TestConcurrentNextClaimsTicketOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, ".local", "tickets.db")
	first, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := Open(context.Background(), root, databasePath, func() time.Time { return fixedNow.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	spec, plan := writeArtifacts(t, root, "only")
	created, err := first.Add(context.Background(), CreateInput{Title: "Only", Summary: "Only summary", SpecPath: spec, PlanPath: plan})
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		ticket Ticket
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, store := range []*Store{first, second} {
		go func(store *Store) {
			<-start
			ticket, err := store.Next(context.Background())
			results <- result{ticket: ticket, err: err}
		}(store)
	}
	close(start)

	claimed := 0
	empty := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			claimed++
			if result.ticket.ID != created.ID || result.ticket.Status != StatusInProgress {
				t.Fatalf("claimed ticket = %#v", result.ticket)
			}
		case errors.Is(result.err, ErrEmptyQueue):
			empty++
		default:
			t.Fatalf("Next() error = %v", result.err)
		}
	}
	if claimed != 1 || empty != 1 {
		t.Fatalf("claimed=%d empty=%d, want 1 each", claimed, empty)
	}
}

func TestLifecycleTransitionsUseAdvancingClockAndResetTimestamps(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := fixedNow
	store, err := Open(context.Background(), root, filepath.Join(root, ".local", "tickets.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spec, plan := writeArtifacts(t, root, "flow")
	created, err := store.Add(context.Background(), CreateInput{Title: "Flow", Summary: "Flow summary", SpecPath: spec, PlanPath: plan})
	if err != nil {
		t.Fatal(err)
	}
	if !created.CreatedAt.Equal(fixedNow) || !created.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("created = %#v", created)
	}

	now = fixedNow.Add(time.Minute)
	started, err := store.Transition(context.Background(), created.ID, ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != StatusInProgress || started.StartedAt == nil || !started.StartedAt.Equal(now) || !started.UpdatedAt.Equal(now) || !started.CreatedAt.Equal(fixedNow) {
		t.Fatalf("start = %#v", started)
	}

	now = fixedNow.Add(2 * time.Minute)
	done, err := store.Transition(context.Background(), created.ID, ActionDone)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusDone || done.StartedAt == nil || !done.StartedAt.Equal(fixedNow.Add(time.Minute)) || done.CompletedAt == nil || !done.CompletedAt.Equal(now) || done.CancelledAt != nil || !done.UpdatedAt.Equal(now) {
		t.Fatalf("done = %#v", done)
	}

	now = fixedNow.Add(3 * time.Minute)
	reopened, err := store.Transition(context.Background(), created.ID, ActionReopen)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != StatusReady || reopened.StartedAt != nil || reopened.CompletedAt != nil || reopened.CancelledAt != nil || !reopened.UpdatedAt.Equal(now) {
		t.Fatalf("reopen = %#v", reopened)
	}

	now = fixedNow.Add(4 * time.Minute)
	cancelled, err := store.Transition(context.Background(), created.ID, ActionCancel)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != StatusCancelled || cancelled.StartedAt != nil || cancelled.CompletedAt != nil || cancelled.CancelledAt == nil || !cancelled.CancelledAt.Equal(now) || !cancelled.UpdatedAt.Equal(now) {
		t.Fatalf("cancel = %#v", cancelled)
	}

	now = fixedNow.Add(5 * time.Minute)
	reopened, err = store.Transition(context.Background(), created.ID, ActionReopen)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != StatusReady || reopened.StartedAt != nil || reopened.CompletedAt != nil || reopened.CancelledAt != nil || !reopened.UpdatedAt.Equal(now) {
		t.Fatalf("second reopen = %#v", reopened)
	}
}

func TestLifecycleAllowsCancellationAndReopening(t *testing.T) {
	cases := []struct {
		name    string
		actions []Action
		status  Status
	}{
		{name: "ready to cancelled", actions: []Action{ActionCancel}, status: StatusCancelled},
		{name: "in progress to cancelled", actions: []Action{ActionStart, ActionCancel}, status: StatusCancelled},
		{name: "cancelled to ready", actions: []Action{ActionCancel, ActionReopen}, status: StatusReady},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, root := newTestStore(t)
			spec, plan := writeArtifacts(t, root, "flow")
			created, err := store.Add(context.Background(), CreateInput{Title: "Flow", Summary: "Flow summary", SpecPath: spec, PlanPath: plan})
			if err != nil {
				t.Fatal(err)
			}

			var got Ticket
			for _, action := range tc.actions {
				got, err = store.Transition(context.Background(), created.ID, action)
				if err != nil {
					t.Fatal(err)
				}
			}
			if got.Status != tc.status {
				t.Fatalf("Transition() status = %q, want %q", got.Status, tc.status)
			}
			if tc.status == StatusCancelled && (got.CancelledAt == nil || !got.CancelledAt.Equal(fixedNow)) {
				t.Fatalf("cancelled ticket = %#v", got)
			}
			if tc.status == StatusReady && (got.StartedAt != nil || got.CompletedAt != nil || got.CancelledAt != nil) {
				t.Fatalf("reopened ticket = %#v", got)
			}
		})
	}
}

func TestConcurrentTransitionsFromIndependentStoresSerialize(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, ".local", "tickets.db")
	firstHasLock := make(chan struct{})
	releaseFirst := make(chan struct{})
	clockCalls := 0
	firstStore, err := Open(context.Background(), root, databasePath, func() time.Time {
		clockCalls++
		if clockCalls == 3 {
			close(firstHasLock)
			<-releaseFirst
		}
		return fixedNow.Add(time.Duration(clockCalls) * time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = firstStore.Close() })

	firstSpec, firstPlan := writeArtifacts(t, root, "concurrent-first")
	first, err := firstStore.Add(context.Background(), CreateInput{
		Title: "First", Summary: "First concurrent transition", SpecPath: firstSpec, PlanPath: firstPlan,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondSpec, secondPlan := writeArtifacts(t, root, "concurrent-second")
	second, err := firstStore.Add(context.Background(), CreateInput{
		Title: "Second", Summary: "Second concurrent transition", SpecPath: secondSpec, PlanPath: secondPlan,
	})
	if err != nil {
		t.Fatal(err)
	}

	secondStore, err := Open(context.Background(), root, databasePath, func() time.Time {
		return fixedNow.Add(10 * time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := firstStore.Transition(ctx, first.ID, ActionStart)
		firstDone <- err
	}()
	select {
	case <-firstHasLock:
	case <-ctx.Done():
		t.Fatalf("first transition did not reach its locked section: %v", ctx.Err())
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, err := secondStore.Transition(ctx, second.ID, ActionStart)
		secondDone <- err
	}()
	<-secondStarted

	var secondErr error
	secondFinished := false
	select {
	case secondErr = <-secondDone:
		secondFinished = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)

	select {
	case err := <-firstDone:
		if err != nil {
			t.Errorf("first transition: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("first transition timed out: %v", ctx.Err())
	}
	if !secondFinished {
		select {
		case secondErr = <-secondDone:
		case <-ctx.Done():
			t.Fatalf("second transition timed out: %v", ctx.Err())
		}
	}
	if secondErr != nil {
		t.Errorf("second transition: %v", secondErr)
	}
}

func TestEveryInvalidTransitionAndActionRollsBack(t *testing.T) {
	allActions := []Action{ActionStart, ActionDone, ActionCancel, ActionReopen, Action("unknown")}
	states := []struct {
		status Status
		setup  []Action
		valid  map[Action]bool
	}{
		{status: StatusReady, valid: map[Action]bool{ActionStart: true, ActionCancel: true}},
		{status: StatusInProgress, setup: []Action{ActionStart}, valid: map[Action]bool{ActionDone: true, ActionCancel: true}},
		{status: StatusDone, setup: []Action{ActionStart, ActionDone}, valid: map[Action]bool{ActionReopen: true}},
		{status: StatusCancelled, setup: []Action{ActionCancel}, valid: map[Action]bool{ActionReopen: true}},
	}

	for _, state := range states {
		for _, action := range allActions {
			if state.valid[action] {
				continue
			}
			t.Run(string(state.status)+"/"+string(action), func(t *testing.T) {
				store, root := newTestStore(t)
				spec, plan := writeArtifacts(t, root, "invalid")
				created, err := store.Add(context.Background(), CreateInput{Title: "Invalid", Summary: "Invalid transition", SpecPath: spec, PlanPath: plan})
				if err != nil {
					t.Fatal(err)
				}
				for _, setupAction := range state.setup {
					if _, err := store.Transition(context.Background(), created.ID, setupAction); err != nil {
						t.Fatalf("setup %q: %v", setupAction, err)
					}
				}
				before, err := store.Get(context.Background(), created.ID)
				if err != nil {
					t.Fatal(err)
				}

				if _, err := store.Transition(context.Background(), created.ID, action); !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("Transition(%q, %q) error = %v, want ErrInvalidTransition", state.status, action, err)
				}
				after, err := store.Get(context.Background(), created.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("invalid transition mutated ticket: before=%#v after=%#v", before, after)
				}
			})
		}
	}
}

func TestTransitionRejectsUnknownIDsAndActions(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "flow")
	created, err := store.Add(context.Background(), CreateInput{Title: "Flow", Summary: "Flow summary", SpecPath: spec, PlanPath: plan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), created.ID+1, ActionStart); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Transition(unknown ID) error = %v, want ErrNotFound", err)
	}
	if _, err := store.Transition(context.Background(), created.ID, Action("unknown")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Transition(unknown action) error = %v, want ErrInvalidTransition", err)
	}
	after, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != created {
		t.Fatalf("unknown action mutated ticket: before=%#v after=%#v", created, after)
	}
}

func statusPtr(status Status) *Status { return &status }
