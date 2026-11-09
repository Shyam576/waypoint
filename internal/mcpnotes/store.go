package mcpnotes

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Store struct{}

func NewStore() *Store { return &Store{} }

type CreateChangeNoteParams struct {
	ReleaseID       uuid.UUID
	AuthorID        uuid.UUID
	Summary         string
	Rationale       string
	AffectedService string
	TestsRun        string
	TestsStatus     TestsStatus
	LinkedPRURL     *string
	LinkedCommitSHA *string
	Source          Source
	MCPSubmissionID *uuid.UUID
}

func (s *Store) CreateChangeNote(ctx context.Context, q dbx.Querier, p CreateChangeNoteParams) (ChangeNote, error) {
	var c ChangeNote
	err := q.QueryRow(ctx, `
		INSERT INTO change_notes (release_id, author_id, summary, rationale, affected_service, tests_run, tests_status, linked_pr_url, linked_commit_sha, source, mcp_submission_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, release_id, author_id, summary, rationale, affected_service, tests_run, tests_status, linked_pr_url, linked_commit_sha, source, mcp_submission_id, created_at`,
		p.ReleaseID, p.AuthorID, p.Summary, p.Rationale, p.AffectedService, p.TestsRun, p.TestsStatus, p.LinkedPRURL, p.LinkedCommitSHA, p.Source, p.MCPSubmissionID,
	).Scan(&c.ID, &c.ReleaseID, &c.AuthorID, &c.Summary, &c.Rationale, &c.AffectedService, &c.TestsRun, &c.TestsStatus, &c.LinkedPRURL, &c.LinkedCommitSHA, &c.Source, &c.MCPSubmissionID, &c.CreatedAt)
	return c, err
}

func (s *Store) ListChangeNotesByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]ChangeNote, error) {
	rows, err := q.Query(ctx, `
		SELECT id, release_id, author_id, summary, rationale, affected_service, tests_run, tests_status, linked_pr_url, linked_commit_sha, source, mcp_submission_id, created_at
		FROM change_notes WHERE release_id = $1 ORDER BY created_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChangeNote{}
	for rows.Next() {
		var c ChangeNote
		if err := rows.Scan(&c.ID, &c.ReleaseID, &c.AuthorID, &c.Summary, &c.Rationale, &c.AffectedService, &c.TestsRun, &c.TestsStatus, &c.LinkedPRURL, &c.LinkedCommitSHA, &c.Source, &c.MCPSubmissionID, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type CreateDesignDecisionParams struct {
	ReleaseID              uuid.UUID
	AuthorID               uuid.UUID
	Decision               string
	Rationale              string
	AlternativesConsidered string
	Impact                 string
	LinkedPRURL            *string
	LinkedCommitSHA        *string
	Source                 Source
	MCPSubmissionID        *uuid.UUID
}

func (s *Store) CreateDesignDecision(ctx context.Context, q dbx.Querier, p CreateDesignDecisionParams) (DesignDecision, error) {
	var d DesignDecision
	err := q.QueryRow(ctx, `
		INSERT INTO design_decisions (release_id, author_id, decision, rationale, alternatives_considered, impact, linked_pr_url, linked_commit_sha, source, mcp_submission_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, release_id, author_id, decision, rationale, alternatives_considered, impact, linked_pr_url, linked_commit_sha, source, mcp_submission_id, created_at`,
		p.ReleaseID, p.AuthorID, p.Decision, p.Rationale, p.AlternativesConsidered, p.Impact, p.LinkedPRURL, p.LinkedCommitSHA, p.Source, p.MCPSubmissionID,
	).Scan(&d.ID, &d.ReleaseID, &d.AuthorID, &d.Decision, &d.Rationale, &d.AlternativesConsidered, &d.Impact, &d.LinkedPRURL, &d.LinkedCommitSHA, &d.Source, &d.MCPSubmissionID, &d.CreatedAt)
	return d, err
}

func (s *Store) ListDesignDecisionsByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]DesignDecision, error) {
	rows, err := q.Query(ctx, `
		SELECT id, release_id, author_id, decision, rationale, alternatives_considered, impact, linked_pr_url, linked_commit_sha, source, mcp_submission_id, created_at
		FROM design_decisions WHERE release_id = $1 ORDER BY created_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DesignDecision{}
	for rows.Next() {
		var d DesignDecision
		if err := rows.Scan(&d.ID, &d.ReleaseID, &d.AuthorID, &d.Decision, &d.Rationale, &d.AlternativesConsidered, &d.Impact, &d.LinkedPRURL, &d.LinkedCommitSHA, &d.Source, &d.MCPSubmissionID, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateSubmission(ctx context.Context, q dbx.Querier, releaseID uuid.UUID, typ SubmissionType, payload []byte, submittedBy uuid.UUID) (Submission, error) {
	var sub Submission
	err := q.QueryRow(ctx, `
		INSERT INTO mcp_submissions (release_id, type, payload, submitted_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, release_id, type, payload, submitted_by, status, reviewed_by, reviewed_at, review_note, created_at`,
		releaseID, typ, payload, submittedBy,
	).Scan(&sub.ID, &sub.ReleaseID, &sub.Type, &sub.Payload, &sub.SubmittedBy, &sub.Status, &sub.ReviewedBy, &sub.ReviewedAt, &sub.ReviewNote, &sub.CreatedAt)
	return sub, err
}

func (s *Store) GetSubmission(ctx context.Context, q dbx.Querier, id uuid.UUID) (Submission, error) {
	var sub Submission
	err := q.QueryRow(ctx, `
		SELECT id, release_id, type, payload, submitted_by, status, reviewed_by, reviewed_at, review_note, created_at
		FROM mcp_submissions WHERE id = $1`, id,
	).Scan(&sub.ID, &sub.ReleaseID, &sub.Type, &sub.Payload, &sub.SubmittedBy, &sub.Status, &sub.ReviewedBy, &sub.ReviewedAt, &sub.ReviewNote, &sub.CreatedAt)
	return sub, err
}

func (s *Store) ListSubmissionsByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID, status *SubmissionStatus) ([]Submission, error) {
	var rows pgx.Rows
	var err error
	if status != nil {
		rows, err = q.Query(ctx, `
			SELECT id, release_id, type, payload, submitted_by, status, reviewed_by, reviewed_at, review_note, created_at
			FROM mcp_submissions WHERE release_id = $1 AND status = $2 ORDER BY created_at DESC`, releaseID, *status)
	} else {
		rows, err = q.Query(ctx, `
			SELECT id, release_id, type, payload, submitted_by, status, reviewed_by, reviewed_at, review_note, created_at
			FROM mcp_submissions WHERE release_id = $1 ORDER BY created_at DESC`, releaseID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Submission{}
	for rows.Next() {
		var sub Submission
		if err := rows.Scan(&sub.ID, &sub.ReleaseID, &sub.Type, &sub.Payload, &sub.SubmittedBy, &sub.Status, &sub.ReviewedBy, &sub.ReviewedAt, &sub.ReviewNote, &sub.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) MarkReviewed(ctx context.Context, q dbx.Querier, id uuid.UUID, status SubmissionStatus, reviewedBy uuid.UUID, reviewNote string) error {
	_, err := q.Exec(ctx, `
		UPDATE mcp_submissions SET status = $2, reviewed_by = $3, reviewed_at = now(), review_note = $4
		WHERE id = $1`, id, status, reviewedBy, reviewNote)
	return err
}
