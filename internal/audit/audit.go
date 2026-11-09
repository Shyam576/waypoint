// Package audit records an append-only history of meaningful business
// events. Nothing in this package exposes an update or delete; the only
// operations are Record (inside the same transaction as the business
// change) and the read helpers used to render a release's activity
// timeline.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Entry struct {
	ID            uuid.UUID       `json:"id"`
	EntityType    string          `json:"entity_type"`
	EntityID      uuid.UUID       `json:"entity_id"`
	Action        string          `json:"action"`
	ActorID       *uuid.UUID      `json:"actor_id,omitempty"`
	ActorLabel    string          `json:"actor_label"`
	PreviousState json.RawMessage `json:"previous_state,omitempty"`
	NewState      json.RawMessage `json:"new_state,omitempty"`
	Remarks       string          `json:"remarks"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Actor identifies who performed an auditable action. ID is nil for
// system/CI actors that are not a Waypoint user account.
type Actor struct {
	ID    *uuid.UUID
	Label string
}

type Store struct{}

func NewStore() *Store { return &Store{} }

// Record writes one append-only audit row. previous/new may be nil.
func (s *Store) Record(ctx context.Context, q dbx.Querier, entityType string, entityID uuid.UUID, action string, actor Actor, previous, next any, remarks string) error {
	var prevJSON, nextJSON []byte
	var err error
	if previous != nil {
		if prevJSON, err = json.Marshal(previous); err != nil {
			return err
		}
	}
	if next != nil {
		if nextJSON, err = json.Marshal(next); err != nil {
			return err
		}
	}

	_, err = q.Exec(ctx, `
		INSERT INTO audit_logs (entity_type, entity_id, action, actor_id, actor_label, previous_state, new_state, remarks)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		entityType, entityID, action, actor.ID, actor.Label, nullableJSON(prevJSON), nullableJSON(nextJSON), remarks)
	return err
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// ForEntity returns the activity timeline for one entity (typically a
// release), newest first.
func (s *Store) ForEntity(ctx context.Context, q dbx.Querier, entityType string, entityID uuid.UUID) ([]Entry, error) {
	rows, err := q.Query(ctx, `
		SELECT id, entity_type, entity_id, action, actor_id, actor_label, previous_state, new_state, remarks, created_at
		FROM audit_logs
		WHERE entity_type = $1 AND entity_id = $2
		ORDER BY created_at DESC`, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.EntityType, &e.EntityID, &e.Action, &e.ActorID, &e.ActorLabel, &e.PreviousState, &e.NewState, &e.Remarks, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
