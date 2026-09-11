package codingagent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"zellij-with-codeagent/internal/persistence"
)

func TestPersistentAgentConcurrentChangesAndDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	w, err := persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewPersistentMemoryStore(w, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(Record{ID: "a", Kind: KindClaude, PaneID: "p", State: StateWorking})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := store.SetPinned("a", i%2 == 0); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
	store.SetTaskAlias("a", TaskAlias("implement persistence"))
	want, _ := store.Get("a")
	_, err = store.Create(Record{ID: "deleted", Kind: KindClaude, PaneID: "gone", State: StateIdle})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Delete("deleted"); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	w, err = persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(context.Background())
	restored, err := NewPersistentMemoryStore(w, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.GetByPane("p")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	if _, err = restored.Get("deleted"); err == nil {
		t.Fatal("deleted agent resurrected")
	}
}
