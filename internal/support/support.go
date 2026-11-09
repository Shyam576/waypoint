// Package support tracks post-go-live issues, fixes, and follow-up
// actions, always linked back to the release that introduced the change
// (the main user flow's "post-go-live support" step).
package support

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

func (s Severity) Valid() bool {
	switch s {
	case SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return true
	}
	return false
}

type ItemStatus string

const (
	StatusOpen          ItemStatus = "open"
	StatusInvestigating ItemStatus = "investigating"
	StatusResolved      ItemStatus = "resolved"
	StatusClosed        ItemStatus = "closed"
)

func (s ItemStatus) Valid() bool {
	switch s {
	case StatusOpen, StatusInvestigating, StatusResolved, StatusClosed:
		return true
	}
	return false
}

type Source string

const (
	SourceWeb Source = "web"
	SourceMCP Source = "mcp"
)

type Item struct {
	ID              uuid.UUID  `json:"id"`
	ReleaseID       uuid.UUID  `json:"release_id"`
	Summary         string     `json:"summary"`
	Severity        Severity   `json:"severity"`
	Status          ItemStatus `json:"status"`
	OwnerID         *uuid.UUID `json:"owner_id,omitempty"`
	LinkedTicketURL *string    `json:"linked_ticket_url,omitempty"`
	LinkedPRURL     *string    `json:"linked_pr_url,omitempty"`
	ResolutionNote  string     `json:"resolution_note"`
	Source          Source     `json:"source"`
	MCPSubmissionID *uuid.UUID `json:"mcp_submission_id,omitempty"`
	CreatedBy       uuid.UUID  `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ClosedAt        *time.Time `json:"closed_at,omitempty"`
}

type Store struct{}

func NewStore() *Store { return &Store{} }

type CreateParams struct {
	ReleaseID       uuid.UUID
	Summary         string
	Severity        Severity
	OwnerID         *uuid.UUID
	LinkedTicketURL *string
	LinkedPRURL     *string
	Source          Source
	MCPSubmissionID *uuid.UUID
	CreatedBy       uuid.UUID
}

func (s *Store) Create(ctx context.Context, q dbx.Querier, p CreateParams) (Item, error) {
	if p.Source == "" {
		p.Source = SourceWeb
	}
	var it Item
	err := q.QueryRow(ctx, `
		INSERT INTO support_items (release_id, summary, severity, owner_id, linked_ticket_url, linked_pr_url, source, mcp_submission_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, release_id, summary, severity, status, owner_id, linked_ticket_url, linked_pr_url, resolution_note, source, mcp_submission_id, created_by, created_at, updated_at, closed_at`,
		p.ReleaseID, p.Summary, p.Severity, p.OwnerID, p.LinkedTicketURL, p.LinkedPRURL, p.Source, p.MCPSubmissionID, p.CreatedBy,
	).Scan(&it.ID, &it.ReleaseID, &it.Summary, &it.Severity, &it.Status, &it.OwnerID, &it.LinkedTicketURL, &it.LinkedPRURL, &it.ResolutionNote, &it.Source, &it.MCPSubmissionID, &it.CreatedBy, &it.CreatedAt, &it.UpdatedAt, &it.ClosedAt)
	return it, err
}

func (s *Store) GetByID(ctx context.Context, q dbx.Querier, id uuid.UUID) (Item, error) {
	var it Item
	err := q.QueryRow(ctx, `
		SELECT id, release_id, summary, severity, status, owner_id, linked_ticket_url, linked_pr_url, resolution_note, source, mcp_submission_id, created_by, created_at, updated_at, closed_at
		FROM support_items WHERE id = $1`, id,
	).Scan(&it.ID, &it.ReleaseID, &it.Summary, &it.Severity, &it.Status, &it.OwnerID, &it.LinkedTicketURL, &it.LinkedPRURL, &it.ResolutionNote, &it.Source, &it.MCPSubmissionID, &it.CreatedBy, &it.CreatedAt, &it.UpdatedAt, &it.ClosedAt)
	return it, err
}

func (s *Store) ListByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Item, error) {
	rows, err := q.Query(ctx, `
		SELECT id, release_id, summary, severity, status, owner_id, linked_ticket_url, linked_pr_url, resolution_note, source, mcp_submission_id, created_by, created_at, updated_at, closed_at
		FROM support_items WHERE release_id = $1 ORDER BY created_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.ReleaseID, &it.Summary, &it.Severity, &it.Status, &it.OwnerID, &it.LinkedTicketURL, &it.LinkedPRURL, &it.ResolutionNote, &it.Source, &it.MCPSubmissionID, &it.CreatedBy, &it.CreatedAt, &it.UpdatedAt, &it.ClosedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

type UpdateParams struct {
	Severity        Severity
	Status          ItemStatus
	OwnerID         *uuid.UUID
	LinkedTicketURL *string
	LinkedPRURL     *string
	ResolutionNote  string
}

func (s *Store) Update(ctx context.Context, q dbx.Querier, id uuid.UUID, p UpdateParams) (Item, error) {
	var closedAt *time.Time
	if p.Status == StatusClosed {
		t := time.Now()
		closedAt = &t
	}
	var it Item
	err := q.QueryRow(ctx, `
		UPDATE support_items
		SET severity = $2, status = $3, owner_id = $4, linked_ticket_url = $5, linked_pr_url = $6, resolution_note = $7, updated_at = now(),
		    closed_at = COALESCE($8, closed_at)
		WHERE id = $1
		RETURNING id, release_id, summary, severity, status, owner_id, linked_ticket_url, linked_pr_url, resolution_note, source, mcp_submission_id, created_by, created_at, updated_at, closed_at`,
		id, p.Severity, p.Status, p.OwnerID, p.LinkedTicketURL, p.LinkedPRURL, p.ResolutionNote, closedAt,
	).Scan(&it.ID, &it.ReleaseID, &it.Summary, &it.Severity, &it.Status, &it.OwnerID, &it.LinkedTicketURL, &it.LinkedPRURL, &it.ResolutionNote, &it.Source, &it.MCPSubmissionID, &it.CreatedBy, &it.CreatedAt, &it.UpdatedAt, &it.ClosedAt)
	return it, err
}
