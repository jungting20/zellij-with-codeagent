package registry

import (
	"encoding/json"
	"fmt"
	"strconv"
	"zellij-with-codeagent/internal/persistence"
)

func tabStorageID(session SessionID, tab TabID) string {
	return strconv.Itoa(len(session)) + ":" + string(session) + string(tab)
}

func (r *Registry) saveLocked(session SessionRecord, tab TabRecord, pane PaneRecord, deleted bool) {
	if r.persist == nil {
		return
	}
	session.Tabs = nil
	tab.Panes = nil
	pane = clonePaneRecord(pane)
	pane.LastOutput = ""
	var value any = pane
	if deleted {
		value = nil
	}
	r.persist.Enqueue(
		persistence.Change{Table: persistence.Sessions, ID: string(session.ID), Value: session},
		persistence.Change{Table: persistence.Tabs, ID: tabStorageID(session.ID, tab.ID), Parent: string(session.ID), Value: tab},
		persistence.Change{Table: persistence.Panes, ID: string(pane.ID), Parent: tabStorageID(session.ID, tab.ID), Value: value},
		persistence.Change{Table: persistence.Metadata, ID: "generation", Value: r.nextGeneration},
	)
}

// NewPersistent restores the hierarchy and rebuilds derived indexes, preserving
// generations even when the most recently registered pane has been deleted.
func NewPersistent(w *persistence.Writer) (*Registry, error) {
	r := New()
	rows, err := w.Load(persistence.Sessions)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var session SessionRecord
		if err = json.Unmarshal(row.Data, &session); err != nil {
			return nil, err
		}
		if session.ID == "" || string(session.ID) != row.ID {
			return nil, fmt.Errorf("invalid stored session %q", row.ID)
		}
		session.Tabs = make(map[TabID]TabRecord)
		r.sessions[session.ID] = session
	}
	rows, err = w.Load(persistence.Tabs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var tab TabRecord
		if err = json.Unmarshal(row.Data, &tab); err != nil {
			return nil, err
		}
		session, ok := r.sessions[SessionID(row.Parent)]
		if !ok || tab.ID == "" || tabStorageID(session.ID, tab.ID) != row.ID {
			return nil, fmt.Errorf("invalid stored tab %q", row.ID)
		}
		tab.Panes = make(map[PaneID]PaneRecord)
		session.Tabs[tab.ID] = tab
	}
	rows, err = w.Load(persistence.Panes)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var pane PaneRecord
		if err = json.Unmarshal(row.Data, &pane); err != nil {
			return nil, err
		}
		session, ok := r.sessions[pane.SessionID]
		tab, tabOK := session.Tabs[pane.TabID]
		if !ok || !tabOK || pane.ID == "" || string(pane.ID) != row.ID || pane.Generation == 0 || row.Parent != tabStorageID(pane.SessionID, pane.TabID) {
			return nil, fmt.Errorf("invalid stored pane %q", row.ID)
		}
		if err = r.validateActiveZellijPaneUniqueLocked(RegisterPaneRequest{SessionID: pane.SessionID, ZellijPaneID: pane.ZellijPaneID}); err != nil && !isTerminalPaneStatus(pane.Status) {
			return nil, err
		}
		pane.LastOutput = ""
		tab.Panes[pane.ID] = pane
		r.paneToLocation[pane.ID] = paneLocation{SessionID: pane.SessionID, TabID: pane.TabID}
		if pane.Generation > r.nextGeneration {
			r.nextGeneration = pane.Generation
		}
	}
	// The legacy unscoped index selects the latest generation across sessions.
	for _, pane := range r.ListPanes() {
		if pane.ZellijPaneID == "" {
			continue
		}
		previous, err := r.GetLatestByZellijPaneID(pane.ZellijPaneID)
		if err != nil || previous.Generation < pane.Generation {
			r.latestByZellij[pane.ZellijPaneID] = pane.ID
		}
	}
	rows, err = w.Load(persistence.Metadata)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID == "generation" {
			var generation uint64
			if err = json.Unmarshal(row.Data, &generation); err != nil {
				return nil, err
			}
			if generation > r.nextGeneration {
				r.nextGeneration = generation
			}
		}
	}
	r.persist = w
	return r, nil
}
