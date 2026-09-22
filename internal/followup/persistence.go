package followup

import (
	"context"
	"encoding/json"
	"fmt"

	"zellij-with-codeagent/internal/persistence"
)

type SQLiteRepository struct{ Writer *persistence.Writer }

func (r SQLiteRepository) Load() ([]Queue, error) {
	rows, err := r.Writer.Load(persistence.Followups)
	if err != nil {
		return nil, err
	}
	queues := make([]Queue, 0, len(rows))
	for _, row := range rows {
		var q Queue
		if err := json.Unmarshal(row.Data, &q); err != nil {
			return nil, fmt.Errorf("load follow-up %s: %w", row.ID, err)
		}
		if q.AgentID != row.ID || q.PaneID != row.PaneID || q.AgentID == "" || q.OwnershipToken == "" {
			return nil, fmt.Errorf("invalid persisted follow-up identity %q", row.ID)
		}
		queues = append(queues, q)
	}
	return queues, nil
}

func (r SQLiteRepository) Save(ctx context.Context, q Queue) error {
	return r.Writer.EnqueueAndWait(ctx, persistence.Change{Table: persistence.Followups, ID: q.AgentID, Parent: q.AgentID, PaneID: q.PaneID, Value: clone(q)})
}
